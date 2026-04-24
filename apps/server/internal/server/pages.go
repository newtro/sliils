package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/pages"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// Native pages (M10-P1).
//
// Surface:
//   GET    /workspaces/:slug/pages             — list
//   POST   /workspaces/:slug/pages             — create
//   GET    /pages/:id                          — fetch metadata
//   PATCH  /pages/:id                          — rename / move / re-icon
//   DELETE /pages/:id                          — archive (soft delete)
//   POST   /pages/:id/auth                     — issue Y-Sweet client token
//   GET    /pages/:id/snapshots                — version history
//   POST   /pages/:id/snapshots                — manual snapshot
//   POST   /pages/:id/snapshots/:sid/restore   — roll page back
//   GET    /pages/:id/comments                 — comment list
//   POST   /pages/:id/comments                 — add comment (optional parent_id + anchor)
//   PATCH  /comments/:cid                      — edit body or resolve
//   DELETE /comments/:cid                      — soft delete
//
// Content streaming is out-of-band: browsers connect directly to Y-Sweet's
// WebSocket for live sync; the Go server only owns metadata, auth-token
// issuance, and snapshot persistence.

// ---- DTOs ---------------------------------------------------------------

type PageDTO struct {
	ID              int64     `json:"id"`
	WorkspaceID     int64     `json:"workspace_id"`
	ChannelID       *int64    `json:"channel_id,omitempty"`
	Title           string    `json:"title"`
	Icon            string    `json:"icon,omitempty"`
	DocID           string    `json:"doc_id"`
	CreatedBy       *int64    `json:"created_by,omitempty"`
	CreatorName     string    `json:"creator_display_name,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
	ArchivedAt      *time.Time `json:"archived_at,omitempty"`
}

type CreatePageRequest struct {
	Title     string `json:"title,omitempty"`
	Icon      string `json:"icon,omitempty"`
	ChannelID *int64 `json:"channel_id,omitempty"`
}

type PatchPageRequest struct {
	Title     *string `json:"title,omitempty"`
	Icon      *string `json:"icon,omitempty"`
	ChannelID *int64  `json:"channel_id,omitempty"`
	ClearChannel bool `json:"clear_channel,omitempty"`
}

type AuthResponse struct {
	URL       string    `json:"url"`
	BaseURL   string    `json:"base_url"`
	DocID     string    `json:"doc_id"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SnapshotDTO struct {
	ID          int64     `json:"id"`
	PageID      int64     `json:"page_id"`
	ByteSize    int32     `json:"byte_size"`
	Reason      string    `json:"reason"`
	CreatedBy   *int64    `json:"created_by,omitempty"`
	CreatorName string    `json:"creator_display_name,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateCommentRequest struct {
	ParentID *int64 `json:"parent_id,omitempty"`
	Anchor   string `json:"anchor,omitempty"`
	BodyMD   string `json:"body_md"`
}

type PatchCommentRequest struct {
	BodyMD   *string `json:"body_md,omitempty"`
	Resolved *bool   `json:"resolved,omitempty"`
}

type CommentDTO struct {
	ID         int64      `json:"id"`
	PageID     int64      `json:"page_id"`
	ParentID   *int64     `json:"parent_id,omitempty"`
	AuthorID   *int64     `json:"author_id,omitempty"`
	AuthorName string     `json:"author_display_name,omitempty"`
	Anchor     string     `json:"anchor,omitempty"`
	BodyMD     string     `json:"body_md"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountPages(api *echo.Group) {
	g := api.Group("")
	g.Use(s.requireAuth())
	g.Use(s.requireTenantWriteLimit())

	// Pages endpoints are always mounted so 404s from bad ids still return
	// a consistent response. The pagesDisabled helper short-circuits with
	// 503 when Y-Sweet isn't wired.
	g.GET("/workspaces/:slug/pages", s.listPages)
	g.POST("/workspaces/:slug/pages", s.createPage)
	g.GET("/pages/:id", s.getPage)
	g.PATCH("/pages/:id", s.patchPage)
	g.DELETE("/pages/:id", s.archivePage)
	g.POST("/pages/:id/auth", s.issuePageAuth)

	g.GET("/pages/:id/snapshots", s.listPageSnapshots)
	g.POST("/pages/:id/snapshots", s.createPageSnapshot)
	g.POST("/pages/:id/snapshots/:sid/restore", s.restorePageSnapshot)

	g.GET("/pages/:id/comments", s.listPageComments)
	g.POST("/pages/:id/comments", s.createPageComment)
	g.PATCH("/comments/:cid", s.patchPageComment)
	g.DELETE("/comments/:cid", s.deletePageComment)
}

// pagesDisabled is the early-exit when the Y-Sweet client isn't wired.
// Endpoints that need live sync call this; pure-metadata endpoints
// (listPages, patchPage) keep working even without a live server.
func (s *Server) pagesDisabled() error {
	return problem.ServiceUnavailable("pages are not configured on this server")
}

// ---- list + get ---------------------------------------------------------

func (s *Server) listPages(c echo.Context) error {
	user := userFromContext(c)
	slug := c.Param("slug")
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	limit := int32(100)
	offset := int32(0)
	if v := c.QueryParam("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			return problem.BadRequest("limit must be 1..500")
		}
		limit = int32(n)
	}
	if v := c.QueryParam("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return problem.BadRequest("offset must be >= 0")
		}
		offset = int32(n)
	}

	var rows []sqlcgen.ListPagesForWorkspaceRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListPagesForWorkspace(c.Request().Context(), sqlcgen.ListPagesForWorkspaceParams{
				WorkspaceID: ws.ID,
				Limit:       limit,
				Offset:      offset,
			})
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list pages: " + err.Error())
	}

	out := make([]PageDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, PageDTO{
			ID:          r.ID,
			WorkspaceID: r.WorkspaceID,
			ChannelID:   r.ChannelID,
			Title:       r.Title,
			Icon:        r.Icon,
			DocID:       r.DocID,
			CreatedBy:   r.CreatedBy,
			CreatorName: derefString(r.CreatorDisplayName),
			CreatedAt:   r.CreatedAt.Time,
			UpdatedAt:   r.UpdatedAt.Time,
			ArchivedAt:  nilIfZero(r.ArchivedAt.Time, r.ArchivedAt.Valid),
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) getPage(c echo.Context) error {
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}

	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pageRowToDTO(page, ""))
}

// ---- create -------------------------------------------------------------

func (s *Server) createPage(c echo.Context) error {
	if s.ySweet == nil {
		return s.pagesDisabled()
	}
	user := userFromContext(c)
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var req CreatePageRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "Untitled"
	}
	req.Icon = strings.TrimSpace(req.Icon)

	// Opaque 128-bit doc id. Prefixed so Y-Sweet's on-disk layout is
	// obviously-SliilS in a mixed-tenant install.
	docID := "sliils-" + randomHex(16)

	// Create the Y-Sweet side FIRST. If that fails we haven't written
	// any metadata and the user retries cleanly. Reverse order would
	// leave an orphan row referring to a doc that doesn't exist.
	if err := s.ySweet.CreateDoc(c.Request().Context(), docID); err != nil {
		return problem.Internal("y-sweet create doc: " + err.Error())
	}

	var created sqlcgen.Page
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			p, err := scope.Queries.CreatePage(c.Request().Context(), sqlcgen.CreatePageParams{
				WorkspaceID: ws.ID,
				ChannelID:   req.ChannelID,
				Title:       req.Title,
				DocID:       docID,
				Icon:        req.Icon,
				CreatedBy:   &user.ID,
			})
			if err != nil {
				return err
			}
			created = p
			return nil
		})
	if err != nil {
		return problem.Internal("create page: " + err.Error())
	}

	s.publishPageEvent("page.created", ws.ID, created.ChannelID, map[string]any{
		"page": pageRowToDTO(&created, ""),
	})

	return c.JSON(http.StatusCreated, pageRowToDTO(&created, ""))
}

// ---- patch / archive ---------------------------------------------------

func (s *Server) patchPage(c echo.Context) error {
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	var req PatchPageRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}

	// Resolve channel_id write value. sqlc.narg for channel_id always
	// writes, so we pass the current value unless the caller explicitly
	// sends a new one or clear_channel=true.
	chanToWrite := page.ChannelID
	if req.ClearChannel {
		chanToWrite = nil
	} else if req.ChannelID != nil {
		chanToWrite = req.ChannelID
	}

	params := sqlcgen.UpdatePageParams{
		ID:        pageID,
		ChannelID: chanToWrite,
	}
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" {
			return problem.BadRequest("title cannot be empty")
		}
		params.Title = &t
	}
	if req.Icon != nil {
		icon := strings.TrimSpace(*req.Icon)
		params.Icon = &icon
	}

	var updated sqlcgen.Page
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID},
		func(scope db.TxScope) error {
			p, err := scope.Queries.UpdatePage(c.Request().Context(), params)
			if err != nil {
				return err
			}
			updated = p
			return nil
		})
	if err != nil {
		return problem.Internal("patch page: " + err.Error())
	}

	s.publishPageEvent("page.updated", updated.WorkspaceID, updated.ChannelID, map[string]any{
		"page": pageRowToDTO(&updated, ""),
	})
	return c.JSON(http.StatusOK, pageRowToDTO(&updated, ""))
}

func (s *Server) archivePage(c echo.Context) error {
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID},
		func(scope db.TxScope) error {
			return scope.Queries.ArchivePage(c.Request().Context(), pageID)
		})
	if err != nil {
		return problem.Internal("archive page: " + err.Error())
	}
	s.publishPageEvent("page.archived", page.WorkspaceID, page.ChannelID, map[string]any{
		"page_id": pageID,
	})
	return c.NoContent(http.StatusNoContent)
}

// ---- Y-Sweet client-token issuance -------------------------------------

func (s *Server) issuePageAuth(c echo.Context) error {
	if s.ySweet == nil {
		return s.pagesDisabled()
	}
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}
	auth, err := s.ySweet.IssueClientAuth(c.Request().Context(), page.DocID)
	if err != nil {
		return problem.Internal("issue client auth: " + err.Error())
	}

	// If SLIILS_YSWEET_PUBLIC_URL is set we rewrite the URL the browser
	// will open. This matters when the app server can talk to Y-Sweet
	// over a private network but the browser has to go through a
	// public reverse proxy.
	url := auth.URL
	baseURL := auth.BaseURL
	if pub := s.cfg.YSweetPublicURL; pub != "" {
		url = rewriteHost(url, pub)
		baseURL = pub
	}

	return c.JSON(http.StatusOK, AuthResponse{
		URL:       url,
		BaseURL:   baseURL,
		DocID:     auth.DocID,
		Token:     auth.Token,
		ExpiresAt: auth.Expires,
	})
}

// ---- snapshots ---------------------------------------------------------

func (s *Server) listPageSnapshots(c echo.Context) error {
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}

	limit := int32(50)
	offset := int32(0)

	var rows []sqlcgen.ListPageSnapshotsRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListPageSnapshots(c.Request().Context(), sqlcgen.ListPageSnapshotsParams{
				PageID: pageID,
				Limit:  limit,
				Offset: offset,
			})
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list snapshots: " + err.Error())
	}
	out := make([]SnapshotDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, SnapshotDTO{
			ID:          r.ID,
			PageID:      r.PageID,
			ByteSize:    r.ByteSize,
			Reason:      r.Reason,
			CreatedBy:   r.CreatedBy,
			CreatorName: derefString(r.CreatorDisplayName),
			CreatedAt:   r.CreatedAt.Time,
		})
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) createPageSnapshot(c echo.Context) error {
	if s.ySweet == nil {
		return s.pagesDisabled()
	}
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}

	data, err := s.ySweet.GetSnapshot(c.Request().Context(), page.DocID)
	if err != nil {
		return problem.Internal("fetch doc state: " + err.Error())
	}
	if len(data) == 0 {
		return problem.BadRequest("document is empty — nothing to snapshot")
	}

	var snap sqlcgen.PageSnapshot
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID},
		func(scope db.TxScope) error {
			p, err := scope.Queries.CreatePageSnapshot(c.Request().Context(), sqlcgen.CreatePageSnapshotParams{
				PageID:       pageID,
				WorkspaceID:  page.WorkspaceID,
				SnapshotData: data,
				ByteSize:     int32(len(data)),
				CreatedBy:    &user.ID,
				Reason:       "manual",
			})
			if err != nil {
				return err
			}
			snap = p
			// Prune older snapshots past retention for this page.
			return scope.Queries.PruneOldSnapshots(c.Request().Context(), sqlcgen.PruneOldSnapshotsParams{
				PageID: pageID,
				Offset: int32(s.cfg.PageSnapshotRetention),
			})
		})
	if err != nil {
		return problem.Internal("create snapshot: " + err.Error())
	}
	return c.JSON(http.StatusCreated, SnapshotDTO{
		ID:        snap.ID,
		PageID:    snap.PageID,
		ByteSize:  snap.ByteSize,
		Reason:    snap.Reason,
		CreatedBy: snap.CreatedBy,
		CreatedAt: snap.CreatedAt.Time,
	})
}

func (s *Server) restorePageSnapshot(c echo.Context) error {
	if s.ySweet == nil {
		return s.pagesDisabled()
	}
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	snapID, err := parseInt64Param(c, "sid")
	if err != nil {
		return err
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}

	var snap sqlcgen.PageSnapshot
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			sn, err := scope.Queries.GetPageSnapshot(c.Request().Context(), sqlcgen.GetPageSnapshotParams{
				ID:     snapID,
				PageID: pageID,
			})
			if err != nil {
				return err
			}
			snap = sn
			return nil
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("snapshot not found")
		}
		return problem.Internal("load snapshot: " + err.Error())
	}

	if err := s.ySweet.ApplyUpdate(c.Request().Context(), page.DocID, snap.SnapshotData); err != nil {
		return problem.Internal("apply snapshot: " + err.Error())
	}

	// Write a "this is what we just restored" snapshot so the log has an
	// audit trail of the rollback.
	_ = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID},
		func(scope db.TxScope) error {
			_, err := scope.Queries.CreatePageSnapshot(c.Request().Context(), sqlcgen.CreatePageSnapshotParams{
				PageID:       pageID,
				WorkspaceID:  page.WorkspaceID,
				SnapshotData: snap.SnapshotData,
				ByteSize:     snap.ByteSize,
				CreatedBy:    &user.ID,
				Reason:       "restore",
			})
			return err
		})

	s.publishPageEvent("page.restored", page.WorkspaceID, page.ChannelID, map[string]any{
		"page_id":     pageID,
		"snapshot_id": snapID,
	})
	return c.NoContent(http.StatusNoContent)
}

// ---- comments ----------------------------------------------------------

func (s *Server) listPageComments(c echo.Context) error {
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}
	var rows []sqlcgen.ListPageCommentsRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListPageComments(c.Request().Context(), pageID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list comments: " + err.Error())
	}
	out := make([]CommentDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, commentRowToDTO(r))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) createPageComment(c echo.Context) error {
	user := userFromContext(c)
	pageID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	var req CreateCommentRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.BodyMD = strings.TrimSpace(req.BodyMD)
	if req.BodyMD == "" {
		return problem.BadRequest("body_md is required")
	}
	page, err := s.loadPageByID(c, user.ID, pageID)
	if err != nil {
		return err
	}
	var created sqlcgen.PageComment
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: page.WorkspaceID},
		func(scope db.TxScope) error {
			p, err := scope.Queries.CreatePageComment(c.Request().Context(), sqlcgen.CreatePageCommentParams{
				PageID:      pageID,
				WorkspaceID: page.WorkspaceID,
				ParentID:    req.ParentID,
				AuthorID:    &user.ID,
				Anchor:      req.Anchor,
				BodyMd:      req.BodyMD,
			})
			if err != nil {
				return err
			}
			created = p
			return nil
		})
	if err != nil {
		return problem.Internal("create comment: " + err.Error())
	}
	// Fan-out for live doc views. Keep payload small — the full list
	// refetches on UI when the event fires.
	s.publishPageEvent("page.comment.added", page.WorkspaceID, page.ChannelID, map[string]any{
		"page_id":    pageID,
		"comment_id": created.ID,
		"author_id":  user.ID,
	})
	return c.JSON(http.StatusCreated, commentRowToDTOFromEntity(&created, user.DisplayName))
}

func (s *Server) patchPageComment(c echo.Context) error {
	user := userFromContext(c)
	cid, err := parseInt64Param(c, "cid")
	if err != nil {
		return err
	}
	var req PatchCommentRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if req.BodyMD == nil && req.Resolved == nil {
		return problem.BadRequest("nothing to update")
	}
	// Load the comment with its workspace so we can scope the tx.
	// There's no direct-by-id RLS-friendly getter; do the update under
	// the workspace of the page the comment belongs to. We can look up
	// the page via a plain pool read since we're immediately going to
	// scope the tx on the workspace id we recovered.
	wsID, pageWorkspaceID, err := s.commentWorkspace(c, user.ID, cid)
	if err != nil {
		return err
	}
	_ = pageWorkspaceID

	params := sqlcgen.UpdatePageCommentParams{ID: cid}
	if req.BodyMD != nil {
		b := strings.TrimSpace(*req.BodyMD)
		if b == "" {
			return problem.BadRequest("body_md cannot be empty")
		}
		params.BodyMd = &b
	}
	if req.Resolved != nil {
		params.Resolved = req.Resolved
	}

	var updated sqlcgen.PageComment
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: wsID},
		func(scope db.TxScope) error {
			p, err := scope.Queries.UpdatePageComment(c.Request().Context(), params)
			if err != nil {
				return err
			}
			updated = p
			return nil
		})
	if err != nil {
		return problem.Internal("update comment: " + err.Error())
	}
	return c.JSON(http.StatusOK, commentRowToDTOFromEntity(&updated, user.DisplayName))
}

func (s *Server) deletePageComment(c echo.Context) error {
	user := userFromContext(c)
	cid, err := parseInt64Param(c, "cid")
	if err != nil {
		return err
	}
	wsID, _, err := s.commentWorkspace(c, user.ID, cid)
	if err != nil {
		return err
	}
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: wsID},
		func(scope db.TxScope) error {
			return scope.Queries.DeletePageComment(c.Request().Context(), cid)
		})
	if err != nil {
		return problem.Internal("delete comment: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- helpers -----------------------------------------------------------

func (s *Server) loadPageByID(c echo.Context, userID, pageID int64) (*sqlcgen.Page, error) {
	// Pages are workspace-scoped under RLS, but RLS only checks
	// workspace_id = app.workspace_id — not whether the caller is a
	// member. So we iterate the user's memberships (which DOES enforce
	// membership via the workspaces RLS) and try the page read under
	// each scope. First hit wins; no hit = 404.
	ctx := c.Request().Context()
	memberships, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return nil, problem.Internal("list workspaces: " + err.Error())
	}
	for _, wsID := range memberships {
		var row sqlcgen.Page
		var found bool
		err := db.WithTx(ctx, s.pool.Pool,
			db.TxOptions{UserID: userID, WorkspaceID: wsID, ReadOnly: true},
			func(scope db.TxScope) error {
				p, err := scope.Queries.GetPageByID(ctx, pageID)
				if err != nil {
					if errors.Is(err, pgx.ErrNoRows) {
						return nil
					}
					return err
				}
				row = p
				found = true
				return nil
			})
		if err != nil {
			return nil, problem.Internal("load page: " + err.Error())
		}
		if found {
			return &row, nil
		}
	}
	return nil, problem.NotFound("page not found")
}

// commentWorkspace resolves the workspace id a comment lives under so
// the caller can scope its tx. Iterates the user's memberships (each
// tenant-scoped) so non-member lookups naturally 404.
func (s *Server) commentWorkspace(c echo.Context, userID, commentID int64) (workspaceID, pageID int64, err error) {
	ctx := c.Request().Context()
	memberships, err := s.listUserWorkspaceIDs(ctx, userID)
	if err != nil {
		return 0, 0, problem.Internal("list workspaces: " + err.Error())
	}
	for _, wsID := range memberships {
		var ws int64
		var pid int64
		var found bool
		err := db.WithTx(ctx, s.pool.Pool,
			db.TxOptions{UserID: userID, WorkspaceID: wsID, ReadOnly: true},
			func(scope db.TxScope) error {
				// Raw SELECT under tenant RLS; no sqlcgen query gets us
				// workspace_id + page_id for a comment by id alone.
				row := scope.Tx.QueryRow(ctx,
					`SELECT workspace_id, page_id FROM page_comments WHERE id = $1 AND deleted_at IS NULL`,
					commentID)
				scanErr := row.Scan(&ws, &pid)
				if scanErr != nil {
					if errors.Is(scanErr, pgx.ErrNoRows) {
						return nil
					}
					return scanErr
				}
				found = true
				return nil
			})
		if err != nil {
			return 0, 0, problem.Internal("load comment: " + err.Error())
		}
		if found {
			return ws, pid, nil
		}
	}
	return 0, 0, problem.NotFound("comment not found")
}

func (s *Server) publishPageEvent(eventType string, workspaceID int64, channelID *int64, payload map[string]any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.broker.Publish(realtime.TopicWorkspace(workspaceID), eventType, b)
	if channelID != nil {
		s.broker.Publish(realtime.TopicChannel(workspaceID, *channelID), eventType, b)
	}
}

func pageRowToDTO(p *sqlcgen.Page, creatorName string) PageDTO {
	var archived *time.Time
	if p.ArchivedAt.Valid {
		t := p.ArchivedAt.Time
		archived = &t
	}
	return PageDTO{
		ID:          p.ID,
		WorkspaceID: p.WorkspaceID,
		ChannelID:   p.ChannelID,
		Title:       p.Title,
		Icon:        p.Icon,
		DocID:       p.DocID,
		CreatedBy:   p.CreatedBy,
		CreatorName: creatorName,
		CreatedAt:   p.CreatedAt.Time,
		UpdatedAt:   p.UpdatedAt.Time,
		ArchivedAt:  archived,
	}
}

func commentRowToDTO(r sqlcgen.ListPageCommentsRow) CommentDTO {
	var resolved *time.Time
	if r.ResolvedAt.Valid {
		t := r.ResolvedAt.Time
		resolved = &t
	}
	return CommentDTO{
		ID:         r.ID,
		PageID:     r.PageID,
		ParentID:   r.ParentID,
		AuthorID:   r.AuthorID,
		AuthorName: derefString(r.AuthorDisplayName),
		Anchor:     r.Anchor,
		BodyMD:     r.BodyMd,
		ResolvedAt: resolved,
		CreatedAt:  r.CreatedAt.Time,
		UpdatedAt:  r.UpdatedAt.Time,
	}
}

func commentRowToDTOFromEntity(c *sqlcgen.PageComment, authorName string) CommentDTO {
	var resolved *time.Time
	if c.ResolvedAt.Valid {
		t := c.ResolvedAt.Time
		resolved = &t
	}
	return CommentDTO{
		ID:         c.ID,
		PageID:     c.PageID,
		ParentID:   c.ParentID,
		AuthorID:   c.AuthorID,
		AuthorName: authorName,
		Anchor:     c.Anchor,
		BodyMD:     c.BodyMd,
		ResolvedAt: resolved,
		CreatedAt:  c.CreatedAt.Time,
		UpdatedAt:  c.UpdatedAt.Time,
	}
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func rewriteHost(original, publicBase string) string {
	// The browser needs a URL whose scheme+host matches what the user's
	// origin can reach. We only rewrite those two parts; the path and
	// query (which contain the Y-Sweet doc routing) come through
	// untouched. If the URL is malformed or publicBase is unusable we
	// return the original — worst case the browser fails the next
	// request and we surface the real error back to the user.
	if original == "" || publicBase == "" {
		return original
	}
	u, err := urlParse(original)
	if err != nil {
		return original
	}
	p, err := urlParse(publicBase)
	if err != nil {
		return original
	}
	// Preserve ws vs wss. Y-Sweet auth returns a ws(s) URL; public URL
	// is often http(s) — map schemes accordingly.
	scheme := p.Scheme
	if u.Scheme == "ws" || u.Scheme == "wss" {
		if p.Scheme == "https" {
			scheme = "wss"
		} else {
			scheme = "ws"
		}
	}
	u.Scheme = scheme
	u.Host = p.Host
	return u.String()
}

// derefString returns the pointer's value or empty string.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nilIfZero(t time.Time, valid bool) *time.Time {
	if !valid {
		return nil
	}
	out := t
	return &out
}

// parseInt64Param parses an integer echo path param and returns 400 on
// failure. Shared enough that promoting into a server-wide helper would
// be nice, but keep local for now to keep the PR scope focused.
func parseInt64Param(c echo.Context, name string) (int64, error) {
	raw := c.Param(name)
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, problem.BadRequest("invalid " + name + ": " + err.Error())
	}
	return v, nil
}

// urlParse is a thin wrapper over net/url.Parse so the import stays in
// this file (the test file exercises rewriteHost in isolation).
var urlParse = pages.ParseURL
