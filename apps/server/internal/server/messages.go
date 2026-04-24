package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// Partition prune window. Every partitioned-table read passes a
// `created_at >= now() - partitionPruneWindow` lower bound so Postgres
// limits the scan to a constant number of partitions.
const partitionPruneWindow = 120 * 24 * time.Hour

// mentionToken matches the Slack/Discord-style raw mention format
// `<@NUMERIC_USER_ID>`. Kept narrow (digits only) so a stray email with
// an @ symbol doesn't get flagged as a mention.
var mentionToken = regexp.MustCompile(`<@(\d+)>`)

// ---- DTOs ---------------------------------------------------------------

type MessageDTO struct {
	ID           int64           `json:"id"`
	ChannelID    int64           `json:"channel_id"`
	WorkspaceID  int64           `json:"workspace_id"`
	AuthorUserID *int64          `json:"author_user_id,omitempty"`
	BodyMD       string          `json:"body_md"`
	BodyBlocks   json.RawMessage `json:"body_blocks"`
	ThreadRootID *int64          `json:"thread_root_id,omitempty"`
	ParentID     *int64          `json:"parent_id,omitempty"`
	ReplyCount   int64           `json:"reply_count"`
	EditedAt     *time.Time      `json:"edited_at,omitempty"`
	DeletedAt    *time.Time      `json:"deleted_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	Reactions    []ReactionDTO   `json:"reactions"`
	Mentions     []int64         `json:"mentions,omitempty"`
	Attachments  []FileDTO       `json:"attachments"`
}

type ReactionDTO struct {
	Emoji   string  `json:"emoji"`
	UserIDs []int64 `json:"user_ids"`
}

type createMessageRequest struct {
	BodyMD        string  `json:"body_md"`
	ParentID      *int64  `json:"parent_id,omitempty"`      // for thread replies
	AttachmentIDs []int64 `json:"attachment_ids,omitempty"` // previously-uploaded file ids
}

type updateMessageRequest struct {
	BodyMD string `json:"body_md"`
}

type reactionRequest struct {
	Emoji string `json:"emoji"`
}

type markReadRequest struct {
	MessageID int64 `json:"message_id"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountMessages(api *echo.Group) {
	g := api.Group("")
	g.Use(s.requireAuth())
	g.POST("/channels/:channel_id/messages", s.createMessage)
	g.GET("/channels/:channel_id/messages", s.listChannelMessages)
	g.POST("/channels/:channel_id/mark-read", s.markRead)
	g.GET("/messages/:id/thread", s.getThread)
	g.PATCH("/messages/:id", s.updateMessage)
	g.DELETE("/messages/:id", s.deleteMessage)
	g.POST("/messages/:id/reactions", s.addReaction)
	g.DELETE("/messages/:id/reactions", s.removeReaction)
}

// ---- create -------------------------------------------------------------

func (s *Server) createMessage(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	channelID, err := parsePathInt64(c, "channel_id")
	if err != nil {
		return problem.BadRequest("invalid channel_id")
	}

	var req createMessageRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.BodyMD = strings.TrimSpace(req.BodyMD)
	if req.BodyMD == "" {
		return problem.BadRequest("message body is required")
	}
	if len(req.BodyMD) > 40_000 {
		return problem.BadRequest("message too long (40000 chars max)")
	}

	workspaceID, err := s.resolveChannelWorkspace(c.Request().Context(), user.ID, channelID)
	if err != nil {
		return err
	}

	// Resolve thread position from parent_id (if any). The parent dictates
	// the root: if parent is itself a root, parent.id IS the root; if parent
	// is already a reply, inherit its thread_root_id.
	var threadRoot *int64
	var parent *int64
	if req.ParentID != nil {
		p, err := s.fetchMessageInWorkspace(c.Request().Context(), user.ID, workspaceID, *req.ParentID)
		if err != nil {
			return err
		}
		if p.ChannelID != channelID {
			return problem.BadRequest("parent message is in a different channel")
		}
		root := p.ID
		if p.ThreadRootID != nil {
			root = *p.ThreadRootID
		}
		threadRoot = &root
		pid := p.ID
		parent = &pid
	}

	var created sqlcgen.Message
	mentionedIDs := extractMentionTokens(req.BodyMD)
	var resolvedMentions []int64
	var attachmentDTOs []FileDTO

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			m, err := scope.Queries.CreateMessage(c.Request().Context(), sqlcgen.CreateMessageParams{
				WorkspaceID:  workspaceID,
				ChannelID:    channelID,
				AuthorUserID: &user.ID,
				BodyMd:       req.BodyMD,
				BodyBlocks:   []byte(`[]`),
				ThreadRootID: threadRoot,
				ParentID:     parent,
			})
			if err != nil {
				return err
			}
			created = m

			// Validate mentioned ids are actually in this workspace before
			// persisting them (prevents pinging arbitrary accounts by id).
			if len(mentionedIDs) > 0 {
				valid, err := scope.Queries.LookupWorkspaceMembersByIDs(c.Request().Context(), sqlcgen.LookupWorkspaceMembersByIDsParams{
					WorkspaceID: workspaceID,
					Column2:     mentionedIDs,
				})
				if err != nil {
					return err
				}
				resolvedMentions = make([]int64, 0, len(valid))
				for _, v := range valid {
					resolvedMentions = append(resolvedMentions, v.UserID)
					if err := scope.Queries.CreateMention(c.Request().Context(), sqlcgen.CreateMentionParams{
						WorkspaceID:      workspaceID,
						ChannelID:        channelID,
						MessageID:        m.ID,
						MessageCreatedAt: m.CreatedAt,
						MentionedUserID:  v.UserID,
						AuthorUserID:     &user.ID,
					}); err != nil {
						return err
					}
				}
			}

			if err := enqueueSearchIndex(c.Request().Context(), scope, workspaceID, m.ID); err != nil {
				return err
			}

			// Attach any previously-uploaded files. The RLS policy on files
			// means GetFileByID only returns rows in the current workspace;
			// a cross-workspace id silently drops.
			if len(req.AttachmentIDs) > 0 {
				attachmentDTOs = make([]FileDTO, 0, len(req.AttachmentIDs))
				for i, fileID := range req.AttachmentIDs {
					f, err := scope.Queries.GetFileByID(c.Request().Context(), fileID)
					if err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							continue
						}
						return err
					}
					if _, err := scope.Queries.CreateAttachment(c.Request().Context(), sqlcgen.CreateAttachmentParams{
						WorkspaceID: workspaceID,
						ChannelID:   channelID,
						MessageID:   m.ID,
						FileID:      f.ID,
						Position:    int32(i),
					}); err != nil {
						return err
					}
					attachmentDTOs = append(attachmentDTOs, fileDTOFromRow(&f))
				}
			}
			return nil
		})
	if err != nil {
		return problem.Internal("create message: " + err.Error())
	}

	dto := messageFromRow(&created, nil, 0)
	dto.Mentions = resolvedMentions
	dto.Attachments = attachmentDTOs
	if dto.Attachments == nil {
		dto.Attachments = []FileDTO{}
	}
	s.publishMessage("message.created", workspaceID, channelID, dto)

	// Mentions trigger a workspace-level event so each recipient gets
	// notified even if they're not currently subscribed to this channel's
	// topic (they will be, via the workspace topic).
	for _, uid := range resolvedMentions {
		payload := map[string]any{
			"message_id": created.ID,
			"channel_id": channelID,
			"user_id":    uid,
			"author_id":  user.ID,
		}
		s.broker.Publish(realtime.TopicWorkspace(workspaceID), "mention.created", mustJSON(payload))
	}
	return c.JSON(http.StatusCreated, dto)
}

// ---- list ---------------------------------------------------------------

type listMessagesResponse struct {
	Messages   []MessageDTO `json:"messages"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type cursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        int64     `json:"id,string"`
}

func (s *Server) listChannelMessages(c echo.Context) error {
	user := userFromContext(c)
	channelID, err := parsePathInt64(c, "channel_id")
	if err != nil {
		return problem.BadRequest("invalid channel_id")
	}

	limit := 50
	if l := c.QueryParam("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	var cursorTime pgtype.Timestamptz
	var cursorID int64
	if raw := c.QueryParam("cursor"); raw != "" {
		decoded, err := decodeCursor(raw)
		if err != nil {
			return problem.BadRequest("invalid cursor")
		}
		cursorTime = pgtype.Timestamptz{Time: decoded.CreatedAt, Valid: true}
		cursorID = decoded.ID
	}

	workspaceID, err := s.resolveChannelWorkspace(c.Request().Context(), user.ID, channelID)
	if err != nil {
		return err
	}

	var rows []sqlcgen.ListChannelMessagesRow
	var reactionRows []sqlcgen.ListReactionsForMessagesRow
	var attachmentRows []sqlcgen.ListAttachmentsForMessagesRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			msgs, err := scope.Queries.ListChannelMessages(c.Request().Context(), sqlcgen.ListChannelMessagesParams{
				ChannelID: channelID,
				Column2:   cursorTime,
				Column3:   cursorID,
				Limit:     int32(limit),
			})
			if err != nil {
				return err
			}
			rows = msgs
			if len(msgs) == 0 {
				return nil
			}
			ids := make([]int64, len(msgs))
			for i, m := range msgs {
				ids[i] = m.ID
			}
			rxs, err := scope.Queries.ListReactionsForMessages(c.Request().Context(), ids)
			if err != nil {
				return err
			}
			reactionRows = rxs

			atts, err := scope.Queries.ListAttachmentsForMessages(c.Request().Context(), ids)
			if err != nil {
				return err
			}
			attachmentRows = atts
			return nil
		})
	if err != nil {
		return problem.Internal("list messages: " + err.Error())
	}

	byMessage := make(map[int64][]ReactionDTO, len(rows))
	for _, r := range reactionRows {
		byMessage[r.MessageID] = append(byMessage[r.MessageID], ReactionDTO{
			Emoji:   r.Emoji,
			UserIDs: r.UserIds,
		})
	}
	attByMessage := attachmentsByMessage(attachmentRows)

	out := make([]MessageDTO, 0, len(rows))
	for i := range rows {
		m := messageFromListRow(&rows[i], byMessage[rows[i].ID])
		m.Attachments = attByMessage[rows[i].ID]
		if m.Attachments == nil {
			m.Attachments = []FileDTO{}
		}
		out = append(out, m)
	}

	resp := listMessagesResponse{Messages: out}
	if len(rows) == limit {
		oldest := rows[len(rows)-1]
		resp.NextCursor = encodeCursor(cursor{CreatedAt: oldest.CreatedAt.Time, ID: oldest.ID})
	}
	return c.JSON(http.StatusOK, resp)
}

// ---- thread -------------------------------------------------------------

type threadResponse struct {
	Root    MessageDTO   `json:"root"`
	Replies []MessageDTO `json:"replies"`
}

func (s *Server) getThread(c echo.Context) error {
	user := userFromContext(c)
	rootID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	// Find the root first (under user's workspace GUCs), then list replies.
	rootRow, workspaceID, err := s.fetchMessage(c.Request().Context(), user.ID, rootID)
	if err != nil {
		return err
	}

	lowerBound := pgtype.Timestamptz{Time: time.Now().Add(-partitionPruneWindow), Valid: true}
	var replyRows []sqlcgen.Message
	var reactionRows []sqlcgen.ListReactionsForMessagesRow
	var attachmentRows []sqlcgen.ListAttachmentsForMessagesRow
	var replyCount int64

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			n, err := scope.Queries.CountThreadReplies(c.Request().Context(), &rootID)
			if err != nil {
				return err
			}
			replyCount = n

			replies, err := scope.Queries.ListThreadReplies(c.Request().Context(), sqlcgen.ListThreadRepliesParams{
				ThreadRootID: &rootID,
				CreatedAt:    lowerBound,
			})
			if err != nil {
				return err
			}
			replyRows = replies
			ids := make([]int64, 0, len(replies)+1)
			ids = append(ids, rootRow.ID)
			for _, r := range replies {
				ids = append(ids, r.ID)
			}
			rxs, err := scope.Queries.ListReactionsForMessages(c.Request().Context(), ids)
			if err != nil {
				return err
			}
			reactionRows = rxs
			atts, err := scope.Queries.ListAttachmentsForMessages(c.Request().Context(), ids)
			if err != nil {
				return err
			}
			attachmentRows = atts
			return nil
		})
	if err != nil {
		return problem.Internal("load thread: " + err.Error())
	}

	byMessage := make(map[int64][]ReactionDTO, len(reactionRows))
	for _, r := range reactionRows {
		byMessage[r.MessageID] = append(byMessage[r.MessageID], ReactionDTO{
			Emoji:   r.Emoji,
			UserIDs: r.UserIds,
		})
	}
	attByMessage := attachmentsByMessage(attachmentRows)

	rootDTO := messageFromRow(rootRow, byMessage[rootRow.ID], replyCount)
	rootDTO.Attachments = attByMessage[rootRow.ID]
	if rootDTO.Attachments == nil {
		rootDTO.Attachments = []FileDTO{}
	}

	resp := threadResponse{
		Root:    rootDTO,
		Replies: make([]MessageDTO, 0, len(replyRows)),
	}
	for i := range replyRows {
		r := messageFromRow(&replyRows[i], byMessage[replyRows[i].ID], 0)
		r.Attachments = attByMessage[replyRows[i].ID]
		if r.Attachments == nil {
			r.Attachments = []FileDTO{}
		}
		resp.Replies = append(resp.Replies, r)
	}
	return c.JSON(http.StatusOK, resp)
}

// attachmentsByMessage pivots a flat list of rows into a map keyed by
// message id. Returns DTOs that include the authenticated download URL.
func attachmentsByMessage(rows []sqlcgen.ListAttachmentsForMessagesRow) map[int64][]FileDTO {
	out := make(map[int64][]FileDTO, len(rows))
	for _, r := range rows {
		dto := FileDTO{
			ID:         r.FileID,
			Filename:   r.Filename,
			MIME:       r.Mime,
			SizeBytes:  r.SizeBytes,
			ScanStatus: r.ScanStatus,
			URL:        fmt.Sprintf("/api/v1/files/%d/raw", r.FileID),
			CreatedAt:  r.CreatedAt.Time,
		}
		if r.Width != nil {
			dto.Width = int(*r.Width)
		}
		if r.Height != nil {
			dto.Height = int(*r.Height)
		}
		out[r.MessageID] = append(out[r.MessageID], dto)
	}
	return out
}

// ---- mark-read ----------------------------------------------------------

func (s *Server) markRead(c echo.Context) error {
	user := userFromContext(c)
	channelID, err := parsePathInt64(c, "channel_id")
	if err != nil {
		return problem.BadRequest("invalid channel_id")
	}
	var req markReadRequest
	if err := c.Bind(&req); err != nil || req.MessageID <= 0 {
		return problem.BadRequest("message_id required")
	}
	workspaceID, err := s.resolveChannelWorkspace(c.Request().Context(), user.ID, channelID)
	if err != nil {
		return err
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			return scope.Queries.UpdateLastRead(c.Request().Context(), sqlcgen.UpdateLastReadParams{
				ChannelID:         channelID,
				UserID:            user.ID,
				LastReadMessageID: &req.MessageID,
			})
		})
	if err != nil {
		return problem.Internal("mark read: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- update -------------------------------------------------------------

func (s *Server) updateMessage(c echo.Context) error {
	user := userFromContext(c)
	messageID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	var req updateMessageRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.BodyMD = strings.TrimSpace(req.BodyMD)
	if req.BodyMD == "" {
		return problem.BadRequest("body_md is required")
	}

	existing, workspaceID, err := s.fetchMessage(c.Request().Context(), user.ID, messageID)
	if err != nil {
		return err
	}
	if existing.AuthorUserID == nil || *existing.AuthorUserID != user.ID {
		return problem.Forbidden("only the author can edit this message")
	}

	var updated sqlcgen.Message
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			m, err := scope.Queries.UpdateMessageBody(c.Request().Context(), sqlcgen.UpdateMessageBodyParams{
				ID:         messageID,
				CreatedAt:  existing.CreatedAt,
				BodyMd:     req.BodyMD,
				BodyBlocks: []byte(`[]`),
			})
			if err != nil {
				return err
			}
			updated = m
			return enqueueSearchIndex(c.Request().Context(), scope, workspaceID, m.ID)
		})
	if err != nil {
		return problem.Internal("update message: " + err.Error())
	}

	dto := messageFromRow(&updated, nil, 0)
	s.publishMessage("message.updated", workspaceID, updated.ChannelID, dto)
	return c.JSON(http.StatusOK, dto)
}

// ---- delete -------------------------------------------------------------

func (s *Server) deleteMessage(c echo.Context) error {
	user := userFromContext(c)
	messageID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	existing, workspaceID, err := s.fetchMessage(c.Request().Context(), user.ID, messageID)
	if err != nil {
		return err
	}
	if existing.AuthorUserID == nil || *existing.AuthorUserID != user.ID {
		return problem.Forbidden("only the author can delete this message")
	}

	var deleted sqlcgen.Message
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			m, err := scope.Queries.SoftDeleteMessage(c.Request().Context(), sqlcgen.SoftDeleteMessageParams{
				ID:        messageID,
				CreatedAt: existing.CreatedAt,
			})
			if err != nil {
				return err
			}
			deleted = m
			return enqueueSearchDelete(c.Request().Context(), scope, workspaceID, m.ID)
		})
	if err != nil {
		return problem.Internal("delete message: " + err.Error())
	}

	dto := messageFromRow(&deleted, nil, 0)
	s.publishMessage("message.deleted", workspaceID, deleted.ChannelID, dto)
	return c.JSON(http.StatusOK, dto)
}

// ---- reactions ----------------------------------------------------------

func (s *Server) addReaction(c echo.Context) error {
	return s.mutateReaction(c, true)
}

func (s *Server) removeReaction(c echo.Context) error {
	return s.mutateReaction(c, false)
}

func (s *Server) mutateReaction(c echo.Context, add bool) error {
	user := userFromContext(c)
	messageID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid id")
	}

	var req reactionRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Emoji = strings.TrimSpace(req.Emoji)
	if req.Emoji == "" || len(req.Emoji) > 64 {
		return problem.BadRequest("emoji must be 1-64 chars")
	}

	existing, workspaceID, err := s.fetchMessage(c.Request().Context(), user.ID, messageID)
	if err != nil {
		return err
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: workspaceID},
		func(scope db.TxScope) error {
			if add {
				return scope.Queries.AddReaction(c.Request().Context(), sqlcgen.AddReactionParams{
					MessageID:   messageID,
					UserID:      user.ID,
					Emoji:       req.Emoji,
					WorkspaceID: workspaceID,
				})
			}
			return scope.Queries.RemoveReaction(c.Request().Context(), sqlcgen.RemoveReactionParams{
				MessageID: messageID,
				UserID:    user.ID,
				Emoji:     req.Emoji,
			})
		})
	if err != nil {
		return problem.Internal("mutate reaction: " + err.Error())
	}

	eventType := "reaction.added"
	if !add {
		eventType = "reaction.removed"
	}
	payload := map[string]any{
		"message_id": messageID,
		"channel_id": existing.ChannelID,
		"user_id":    user.ID,
		"emoji":      req.Emoji,
	}
	s.broker.Publish(realtime.TopicChannel(workspaceID, existing.ChannelID), eventType, mustJSON(payload))

	return c.JSON(http.StatusOK, payload)
}

// ---- helpers ------------------------------------------------------------

func (s *Server) resolveChannelWorkspace(ctx context.Context, userID, channelID int64) (int64, error) {
	var workspaceID int64
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, ReadOnly: true}, func(scope db.TxScope) error {
		ch, err := scope.Queries.GetChannelByID(ctx, channelID)
		if err != nil {
			return err
		}
		workspaceID = ch.WorkspaceID
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, problem.NotFound("channel not found")
		}
		return 0, problem.Internal("resolve channel: " + err.Error())
	}
	return workspaceID, nil
}

// fetchMessage finds a message by id without the caller knowing its
// workspace. Uses the user's active memberships to iterate possible GUC
// contexts. Scale-wise fine for M4 (small per-user workspace counts); M6
// search will ship URLs that carry channel context explicitly.
func (s *Server) fetchMessage(ctx context.Context, userID, messageID int64) (*sqlcgen.Message, int64, error) {
	memberships, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return nil, 0, problem.Internal("list workspaces: " + err.Error())
	}
	for _, wsID := range memberships {
		row, found, err := s.tryFetchMessage(ctx, userID, wsID, messageID)
		if err != nil {
			return nil, 0, err
		}
		if found {
			return row, wsID, nil
		}
	}
	return nil, 0, problem.NotFound("message not found")
}

// fetchMessageInWorkspace is the known-workspace variant; used when the
// caller already has app.workspace_id determined (e.g., from the URL).
func (s *Server) fetchMessageInWorkspace(ctx context.Context, userID, workspaceID, messageID int64) (*sqlcgen.Message, error) {
	row, found, err := s.tryFetchMessage(ctx, userID, workspaceID, messageID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, problem.NotFound("message not found")
	}
	return row, nil
}

func (s *Server) tryFetchMessage(ctx context.Context, userID, workspaceID, messageID int64) (*sqlcgen.Message, bool, error) {
	lowerBound := pgtype.Timestamptz{Time: time.Now().Add(-partitionPruneWindow), Valid: true}
	var row sqlcgen.Message
	var found bool
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: workspaceID, ReadOnly: true}, func(scope db.TxScope) error {
		m, err := scope.Queries.GetMessageByID(ctx, sqlcgen.GetMessageByIDParams{
			ID:        messageID,
			CreatedAt: lowerBound,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		row = m
		found = true
		return nil
	})
	if err != nil {
		return nil, false, problem.Internal("fetch message: " + err.Error())
	}
	return &row, found, nil
}

func (s *Server) listUserWorkspaceIDs(ctx context.Context, userID int64) ([]int64, error) {
	var out []int64
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, ReadOnly: true}, func(scope db.TxScope) error {
		rows, err := scope.Queries.ListWorkspacesForUser(ctx, userID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return nil
	})
	return out, err
}

func (s *Server) publishMessage(eventType string, workspaceID, channelID int64, dto MessageDTO) {
	payload, err := json.Marshal(dto)
	if err != nil {
		s.logger.Warn("marshal message event failed", "error", err.Error())
		return
	}
	s.broker.Publish(realtime.TopicChannel(workspaceID, channelID), eventType, payload)
}

// extractMentionTokens parses every `<@N>` token from the message body and
// returns the unique user ids referenced. Invalid non-digits are dropped
// by the regex; duplicates collapse.
func extractMentionTokens(body string) []int64 {
	matches := mentionToken.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(matches))
	out := make([]int64, 0, len(matches))
	for _, m := range matches {
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func messageFromRow(m *sqlcgen.Message, reactions []ReactionDTO, replyCount int64) MessageDTO {
	dto := MessageDTO{
		ID:           m.ID,
		ChannelID:    m.ChannelID,
		WorkspaceID:  m.WorkspaceID,
		AuthorUserID: m.AuthorUserID,
		BodyMD:       m.BodyMd,
		BodyBlocks:   m.BodyBlocks,
		ThreadRootID: m.ThreadRootID,
		ParentID:     m.ParentID,
		ReplyCount:   replyCount,
		CreatedAt:    m.CreatedAt.Time,
		Reactions:    reactions,
	}
	if dto.Reactions == nil {
		dto.Reactions = []ReactionDTO{}
	}
	if m.EditedAt.Valid {
		t := m.EditedAt.Time
		dto.EditedAt = &t
	}
	if m.DeletedAt.Valid {
		t := m.DeletedAt.Time
		dto.DeletedAt = &t
	}
	return dto
}

// messageFromListRow mirrors messageFromRow for ListChannelMessagesRow,
// carrying the joined reply_count from the SQL subquery.
func messageFromListRow(r *sqlcgen.ListChannelMessagesRow, reactions []ReactionDTO) MessageDTO {
	dto := MessageDTO{
		ID:           r.ID,
		ChannelID:    r.ChannelID,
		WorkspaceID:  r.WorkspaceID,
		AuthorUserID: r.AuthorUserID,
		BodyMD:       r.BodyMd,
		BodyBlocks:   r.BodyBlocks,
		ThreadRootID: r.ThreadRootID,
		ParentID:     r.ParentID,
		ReplyCount:   r.ReplyCount,
		CreatedAt:    r.CreatedAt.Time,
		Reactions:    reactions,
	}
	if dto.Reactions == nil {
		dto.Reactions = []ReactionDTO{}
	}
	if r.EditedAt.Valid {
		t := r.EditedAt.Time
		dto.EditedAt = &t
	}
	if r.DeletedAt.Valid {
		t := r.DeletedAt.Time
		dto.DeletedAt = &t
	}
	return dto
}

func parsePathInt64(c echo.Context, name string) (int64, error) {
	return strconv.ParseInt(c.Param(name), 10, 64)
}

// ---- cursor encoding ----------------------------------------------------

func encodeCursor(cur cursor) string {
	b, _ := json.Marshal(cur)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(raw string) (*cursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cur cursor
	if err := json.Unmarshal(b, &cur); err != nil {
		return nil, err
	}
	return &cur, nil
}
