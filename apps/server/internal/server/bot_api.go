package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/apps"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// Bot API (M12-P2).
//
// Slack-shaped endpoints a third-party app hits with its access token:
//
//   POST /api/v1/chat.postMessage       — post as the bot user into a channel
//   GET  /api/v1/auth.test              — verify a token
//
// Views (chat_update / views.open / views.update / views.push) are
// listed in the kickoff but deferred to the interactive surface story
// and will land in v1.1.
//
// Authentication: `Authorization: Bearer slis-xat-{token_id}-{secret}`.
// Middleware parses the token, loads the app_tokens row, verifies the
// hash in constant time, and injects install + scopes onto the context.

type ctxBotTokenKey struct{}
type botTokenContext struct {
	Install sqlcgen.AppInstallation
	Scopes  []string
	TokenID int64
}

// requireBotToken is middleware that validates a `slis-xat-...` bearer.
// Failure = 401 so honest apps get a clean "your token is bad" signal
// without a detailed error they can probe on.
func (s *Server) requireBotToken() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authz := c.Request().Header.Get(echo.HeaderAuthorization)
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				return problem.Unauthorized("missing bearer token")
			}
			raw := strings.TrimPrefix(authz, prefix)
			id, secret, err := apps.ParseAccessToken(raw)
			if err != nil {
				return problem.Unauthorized("malformed token")
			}
			if s.ownerPool == nil {
				return problem.Internal("apps require the owner pool")
			}
			ownerQ := sqlcgen.New(s.ownerPool)
			tok, err := ownerQ.GetAppTokenByID(c.Request().Context(), id)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return problem.Unauthorized("invalid token")
				}
				return problem.Internal("load token: " + err.Error())
			}
			if !apps.VerifyAccessTokenSecret(secret, tok.TokenHash) {
				return problem.Unauthorized("invalid token")
			}
			install, err := ownerQ.GetInstallationByID(c.Request().Context(), tok.AppInstallationID)
			if err != nil {
				return problem.Internal("load install: " + err.Error())
			}
			if install.RevokedAt.Valid {
				return problem.Unauthorized("installation revoked")
			}
			_ = ownerQ.TouchAppToken(c.Request().Context(), tok.TokenID)

			bt := &botTokenContext{
				Install: install,
				Scopes:  apps.DecodeScopes(tok.Scopes),
				TokenID: tok.TokenID,
			}
			ctx := context.WithValue(c.Request().Context(), ctxBotTokenKey{}, bt)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

func botFromContext(c echo.Context) *botTokenContext {
	v, _ := c.Request().Context().Value(ctxBotTokenKey{}).(*botTokenContext)
	return v
}

// ---- routes ------------------------------------------------------------

func (s *Server) mountBotAPI(api *echo.Group) {
	g := api.Group("")
	g.Use(s.requireBotToken())
	g.POST("/chat.postMessage", s.botChatPostMessage)
	g.GET("/auth.test", s.botAuthTest)
}

// ---- handlers ----------------------------------------------------------

type chatPostMessageRequest struct {
	Channel  string        `json:"channel"`  // channel id as string, Slack-style
	Text     string        `json:"text,omitempty"`
	Blocks   []apps.Block  `json:"blocks,omitempty"`
	ThreadTS string        `json:"thread_ts,omitempty"` // parent message id for threading
}

type chatPostMessageResponse struct {
	OK        bool   `json:"ok"`
	Channel   int64  `json:"channel"`
	MessageID int64  `json:"message_id"`
}

func (s *Server) botChatPostMessage(c echo.Context) error {
	bt := botFromContext(c)
	if bt == nil {
		return problem.Unauthorized("missing bot context")
	}
	if !apps.HasScope(bt.Scopes, "chat:write") {
		return problem.Forbidden("token lacks chat:write")
	}
	var req chatPostMessageRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if req.Channel == "" || (req.Text == "" && len(req.Blocks) == 0) {
		return problem.BadRequest("channel and (text or blocks) are required")
	}
	chID, err := parseInt64Channel(req.Channel)
	if err != nil {
		return problem.BadRequest("channel must be a numeric id")
	}

	// The channel must be in the installation's workspace. RLS gives us
	// that check for free — we scope the tx to install.WorkspaceID.
	blocksJSON := apps.EncodeBlocks(req.Blocks)
	if _, err := apps.ValidateBlocksJSON(blocksJSON); err != nil {
		return problem.BadRequest(err.Error())
	}

	var threadRoot, parent *int64
	if req.ThreadTS != "" {
		tr, err := parseInt64Channel(req.ThreadTS)
		if err != nil {
			return problem.BadRequest("thread_ts must be a numeric id")
		}
		threadRoot = &tr
		parent = &tr
	}

	// Bot API uses the owner pool: a `chat:write` scope grants posting
	// anywhere in the installation's workspace, not just channels the
	// bot is explicitly a member of (matches Slack semantics). We still
	// enforce workspace-scope by rejecting channels whose workspace_id
	// doesn't match the installation's.
	if s.ownerPool == nil {
		return problem.Internal("bot api requires the owner pool")
	}
	ownerQ := sqlcgen.New(s.ownerPool)
	ch, err := ownerQ.GetChannelByID(c.Request().Context(), chID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("channel not found")
		}
		return problem.Internal("load channel: " + err.Error())
	}
	if ch.WorkspaceID != bt.Install.WorkspaceID {
		return problem.NotFound("channel not found in this workspace")
	}
	created, err := ownerQ.CreateBotMessage(c.Request().Context(), sqlcgen.CreateBotMessageParams{
		WorkspaceID:             bt.Install.WorkspaceID,
		ChannelID:               chID,
		AuthorUserID:            bt.Install.BotUserID,
		AuthorBotInstallationID: &bt.Install.ID,
		BodyMd:                  req.Text,
		BodyBlocks:              blocksJSON,
		ThreadRootID:            threadRoot,
		ParentID:                parent,
	})
	if err != nil {
		return problem.Internal("post message: " + err.Error())
	}

	// Fan-out realtime so any connected client updates.
	b, _ := json.Marshal(map[string]any{
		"id":         created.ID,
		"channel_id": created.ChannelID,
		"workspace_id": created.WorkspaceID,
		"body_md":    created.BodyMd,
		"author_bot_installation_id": bt.Install.ID,
		"created_at": created.CreatedAt.Time,
	})
	s.broker.Publish(realtime.TopicChannel(created.WorkspaceID, created.ChannelID), "message.created", b)

	return c.JSON(http.StatusOK, chatPostMessageResponse{
		OK:        true,
		Channel:   created.ChannelID,
		MessageID: created.ID,
	})
}

type authTestResponse struct {
	OK           bool     `json:"ok"`
	AppID        int64    `json:"app_id"`
	InstallationID int64  `json:"installation_id"`
	WorkspaceID  int64    `json:"workspace_id"`
	BotUserID    *int64   `json:"bot_user_id,omitempty"`
	Scopes       []string `json:"scopes"`
}

func (s *Server) botAuthTest(c echo.Context) error {
	bt := botFromContext(c)
	if bt == nil {
		return problem.Unauthorized("missing bot context")
	}
	return c.JSON(http.StatusOK, authTestResponse{
		OK:           true,
		AppID:        bt.Install.AppID,
		InstallationID: bt.Install.ID,
		WorkspaceID:  bt.Install.WorkspaceID,
		BotUserID:    bt.Install.BotUserID,
		Scopes:       bt.Scopes,
	})
}

// ---- helpers -----------------------------------------------------------

func derefOr(p *int64, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return *p
}

func parseInt64Channel(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not numeric")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}
