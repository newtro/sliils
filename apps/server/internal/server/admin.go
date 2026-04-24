package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Admin dashboard (M12-P3).
//
// Admin-only surface for a workspace's owner + admin roles:
//
//   GET   /workspaces/:slug/admin/members           — members list
//   PATCH /workspaces/:slug/admin/members/:uid      — role change (owner|admin|member|guest)
//   DELETE /workspaces/:slug/admin/members/:uid     — deactivate
//   GET   /workspaces/:slug/admin/audit             — paginated audit log
//   PATCH /workspaces/:slug/admin/settings          — name / description / brand_color / logo_file_id / retention_days
//   POST  /workspaces/:slug/admin/export            — start a data export (zip)
//   GET   /workspaces/:slug/admin/export/user/:uid  — per-user GDPR export (streaming zip)
//
// All routes enforce requireWorkspaceAdmin. Every mutation writes an
// audit_log row so the viewer endpoint can show "who changed what."

type AdminMemberDTO struct {
	UserID       int64     `json:"user_id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Role         string    `json:"role"`
	JoinedAt     time.Time `json:"joined_at"`
	EmailVerified *time.Time `json:"email_verified_at,omitempty"`
	Deactivated  *time.Time `json:"deactivated_at,omitempty"`
}

type patchMemberRequest struct {
	Role *string `json:"role,omitempty"` // owner|admin|member|guest
}

type AuditEntryDTO struct {
	ID          int64     `json:"id"`
	ActorUserID *int64    `json:"actor_user_id,omitempty"`
	ActorName   string    `json:"actor_display_name,omitempty"`
	ActorEmail  string    `json:"actor_email,omitempty"`
	ActorIP     string    `json:"actor_ip,omitempty"`
	Action      string    `json:"action"`
	TargetKind  string    `json:"target_kind,omitempty"`
	TargetID    string    `json:"target_id,omitempty"`
	Metadata    any       `json:"metadata,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type patchWorkspaceAdminRequest struct {
	Name          *string `json:"name,omitempty"`
	Description   *string `json:"description,omitempty"`
	BrandColor    *string `json:"brand_color,omitempty"`
	LogoFileID    *int64  `json:"logo_file_id,omitempty"`
	RetentionDays *int32  `json:"retention_days,omitempty"` // null-via-missing means "keep forever"
	ClearRetention bool   `json:"clear_retention,omitempty"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountAdmin(api *echo.Group) {
	g := api.Group("/workspaces/:slug/admin")
	g.Use(s.requireAuth())

	g.GET("/members", s.adminListMembers)
	g.PATCH("/members/:uid", s.adminPatchMember)
	g.DELETE("/members/:uid", s.adminDeactivateMember)

	g.GET("/audit", s.adminAuditLog)
	g.PATCH("/settings", s.adminPatchWorkspace)

	g.POST("/export", s.adminWorkspaceExport)
	g.GET("/export/user/:uid", s.adminUserExport)
}

// ---- members -----------------------------------------------------------

func (s *Server) adminListMembers(c echo.Context) error {
	user := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}

	var rows []sqlcgen.ListWorkspaceMembersRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListWorkspaceMembers(c.Request().Context(), ws.ID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list members: " + err.Error())
	}
	out := make([]AdminMemberDTO, 0, len(rows))
	for _, r := range rows {
		dto := AdminMemberDTO{
			UserID:      r.UserID,
			Email:       r.Email,
			DisplayName: r.DisplayName,
			Role:        r.Role,
			JoinedAt:    r.JoinedAt.Time,
		}
		if r.EmailVerifiedAt.Valid {
			t := r.EmailVerifiedAt.Time
			dto.EmailVerified = &t
		}
		if r.DeactivatedAt.Valid {
			t := r.DeactivatedAt.Time
			dto.Deactivated = &t
		}
		out = append(out, dto)
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) adminPatchMember(c echo.Context) error {
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	targetUID, err := parseInt64Param(c, "uid")
	if err != nil {
		return err
	}

	var req patchMemberRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if req.Role == nil {
		return problem.BadRequest("nothing to update")
	}
	switch *req.Role {
	case "owner", "admin", "member", "guest":
	default:
		return problem.BadRequest("role must be owner|admin|member|guest")
	}

	// Promoting someone to owner is fine (multi-owner model). Demoting
	// yourself is allowed too, but there must always be at least one
	// owner. Enforce at tx time via a count check.
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: actor.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			if *req.Role != "owner" && targetUID == actor.ID {
				var ownerCount int
				row := scope.Tx.QueryRow(c.Request().Context(),
					`SELECT count(*) FROM workspace_memberships
					 WHERE workspace_id = $1 AND role = 'owner' AND deactivated_at IS NULL
					   AND user_id <> $2`,
					ws.ID, actor.ID)
				if err := row.Scan(&ownerCount); err != nil {
					return err
				}
				if ownerCount == 0 {
					return errLastOwner
				}
			}
			return scope.Queries.UpdateMembershipRole(c.Request().Context(), sqlcgen.UpdateMembershipRoleParams{
				WorkspaceID: ws.ID,
				UserID:      targetUID,
				Role:        *req.Role,
			})
		})
	if err != nil {
		if errors.Is(err, errLastOwner) {
			return problem.Conflict("cannot demote the last owner")
		}
		return problem.Internal("update role: " + err.Error())
	}
	s.recordAudit(c, ws.ID, actor.ID, "member.role_changed", "user", &targetUID, map[string]any{
		"new_role": *req.Role,
	})
	return c.NoContent(http.StatusNoContent)
}

var errLastOwner = errors.New("last owner")

func (s *Server) adminDeactivateMember(c echo.Context) error {
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	targetUID, err := parseInt64Param(c, "uid")
	if err != nil {
		return err
	}
	if targetUID == actor.ID {
		return problem.BadRequest("cannot deactivate yourself — have another owner do it")
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: actor.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			return scope.Queries.DeactivateMembership(c.Request().Context(), sqlcgen.DeactivateMembershipParams{
				WorkspaceID: ws.ID,
				UserID:      targetUID,
			})
		})
	if err != nil {
		return problem.Internal("deactivate: " + err.Error())
	}
	s.recordAudit(c, ws.ID, actor.ID, "member.deactivated", "user", &targetUID, nil)
	return c.NoContent(http.StatusNoContent)
}

// ---- audit log ---------------------------------------------------------

func (s *Server) adminAuditLog(c echo.Context) error {
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
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

	// Audit rows are NOT workspace-tenant-scoped under RLS (they sit
	// outside the normal tenant tables). Use the owner pool.
	if s.ownerPool == nil {
		return problem.Internal("audit requires the owner pool")
	}
	ownerQ := sqlcgen.New(s.ownerPool)
	rows, err := ownerQ.ListAuditLogForWorkspace(c.Request().Context(), sqlcgen.ListAuditLogForWorkspaceParams{
		WorkspaceID: &ws.ID,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return problem.Internal("audit: " + err.Error())
	}
	out := make([]AuditEntryDTO, 0, len(rows))
	for _, r := range rows {
		entry := AuditEntryDTO{
			ID:          r.ID,
			ActorUserID: r.ActorUserID,
			Action:      r.Action,
			CreatedAt:   r.CreatedAt.Time,
		}
		if r.ActorDisplayName != nil {
			entry.ActorName = *r.ActorDisplayName
		}
		if r.ActorEmail != nil {
			entry.ActorEmail = *r.ActorEmail
		}
		if r.TargetKind != nil {
			entry.TargetKind = *r.TargetKind
		}
		if r.TargetID != nil {
			entry.TargetID = *r.TargetID
		}
		if r.ActorIp != nil {
			entry.ActorIP = r.ActorIp.String()
		}
		if len(r.Metadata) > 0 {
			var m any
			_ = json.Unmarshal(r.Metadata, &m)
			entry.Metadata = m
		}
		out = append(out, entry)
	}
	return c.JSON(http.StatusOK, out)
}

// ---- workspace settings -----------------------------------------------

func (s *Server) adminPatchWorkspace(c echo.Context) error {
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	var req patchWorkspaceAdminRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	params := sqlcgen.UpdateWorkspaceAdminParams{ID: ws.ID}
	if req.Name != nil {
		t := strings.TrimSpace(*req.Name)
		if t == "" {
			return problem.BadRequest("name cannot be empty")
		}
		params.Name = &t
	}
	if req.Description != nil {
		params.Description = req.Description
	}
	if req.BrandColor != nil {
		params.BrandColor = req.BrandColor
	}
	if req.LogoFileID != nil {
		params.LogoFileID = req.LogoFileID
	}
	// retention_days: passed through as-is. Callers that want "forever"
	// set ClearRetention=true or simply omit the field with
	// RetentionDays=nil (our sqlc.narg stores NULL).
	if req.ClearRetention {
		params.RetentionDays = nil
	} else {
		params.RetentionDays = req.RetentionDays
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: actor.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			_, err := scope.Queries.UpdateWorkspaceAdmin(c.Request().Context(), params)
			return err
		})
	if err != nil {
		return problem.Internal("update workspace: " + err.Error())
	}
	meta := map[string]any{}
	if req.RetentionDays != nil {
		meta["retention_days"] = *req.RetentionDays
	}
	s.recordAudit(c, ws.ID, actor.ID, "workspace.settings_updated", "workspace", &ws.ID, meta)
	return c.NoContent(http.StatusNoContent)
}

// ---- data export -------------------------------------------------------

// adminWorkspaceExport builds a zip containing:
//   workspace.json    — basic metadata
//   members.json      — roster
//   channels.json     — channel list
//   messages.ndjson   — one JSON message per line (streaming-friendly)
//
// For v1 we stream directly to the response. Large workspaces would
// want an async job that uploads to SeaweedFS and emails a link when
// ready; tracked against v1.1.
func (s *Server) adminWorkspaceExport(c echo.Context) error {
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	if s.ownerPool == nil {
		return problem.Internal("export requires the owner pool")
	}

	c.Response().Header().Set("Content-Type", "application/zip")
	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, ws.Slug+"-export.zip"))
	zw := zip.NewWriter(c.Response().Writer)
	defer zw.Close()

	if err := writeJSONEntry(zw, "workspace.json", map[string]any{
		"id":         ws.ID,
		"slug":       ws.Slug,
		"name":       ws.Name,
		"created_at": ws.CreatedAt.Time,
	}); err != nil {
		return err
	}

	// Members
	membersCtx, cancel := context.WithTimeout(c.Request().Context(), 30*time.Second)
	defer cancel()
	ownerQ := sqlcgen.New(s.ownerPool)
	rows, err := s.ownerPool.Query(membersCtx,
		`SELECT m.user_id, u.email, u.display_name, m.role, m.joined_at
		 FROM workspace_memberships m JOIN users u ON u.id = m.user_id
		 WHERE m.workspace_id = $1`, ws.ID)
	if err != nil {
		return err
	}
	type member struct {
		UserID      int64     `json:"user_id"`
		Email       string    `json:"email"`
		DisplayName string    `json:"display_name"`
		Role        string    `json:"role"`
		JoinedAt    time.Time `json:"joined_at"`
	}
	var members []member
	for rows.Next() {
		var m member
		var joined time.Time
		if err := rows.Scan(&m.UserID, &m.Email, &m.DisplayName, &m.Role, &joined); err != nil {
			rows.Close()
			return err
		}
		m.JoinedAt = joined
		members = append(members, m)
	}
	rows.Close()
	if err := writeJSONEntry(zw, "members.json", members); err != nil {
		return err
	}

	// Channels. `name` is CITEXT and nullable (DM channels have no
	// name), so we scan into a *string.
	chRows, err := s.ownerPool.Query(membersCtx,
		`SELECT id, name, type, topic, created_at
		 FROM channels WHERE workspace_id = $1 AND archived_at IS NULL`, ws.ID)
	if err != nil {
		return err
	}
	type channel struct {
		ID        int64     `json:"id"`
		Name      string    `json:"name,omitempty"`
		Type      string    `json:"type"`
		Topic     string    `json:"topic,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	var channels []channel
	for chRows.Next() {
		var (
			id         int64
			name       *string
			typ, topic string
			createdAt  time.Time
		)
		if err := chRows.Scan(&id, &name, &typ, &topic, &createdAt); err != nil {
			chRows.Close()
			return err
		}
		ch := channel{ID: id, Type: typ, Topic: topic, CreatedAt: createdAt}
		if name != nil {
			ch.Name = *name
		}
		channels = append(channels, ch)
	}
	chRows.Close()
	if err := writeJSONEntry(zw, "channels.json", channels); err != nil {
		return err
	}

	// Messages — NDJSON so large exports don't have to fit in memory.
	msgsEntry, err := zw.Create("messages.ndjson")
	if err != nil {
		return err
	}
	msgRows, err := s.ownerPool.Query(membersCtx,
		`SELECT id, channel_id, author_user_id, author_bot_installation_id,
		        body_md, body_blocks, created_at
		 FROM messages WHERE workspace_id = $1 AND deleted_at IS NULL
		 ORDER BY id`, ws.ID)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(msgsEntry)
	for msgRows.Next() {
		var (
			id, channelID int64
			authorUser    *int64
			botInstall    *int64
			bodyMD        string
			bodyBlocks    []byte
			createdAt     time.Time
		)
		if err := msgRows.Scan(&id, &channelID, &authorUser, &botInstall, &bodyMD, &bodyBlocks, &createdAt); err != nil {
			msgRows.Close()
			return err
		}
		var blocksAny any
		if len(bodyBlocks) > 0 {
			_ = json.Unmarshal(bodyBlocks, &blocksAny)
		}
		_ = enc.Encode(map[string]any{
			"id":                        id,
			"channel_id":                channelID,
			"author_user_id":            authorUser,
			"author_bot_installation_id": botInstall,
			"body_md":                   bodyMD,
			"body_blocks":               blocksAny,
			"created_at":                createdAt,
		})
	}
	msgRows.Close()

	s.recordAudit(c, ws.ID, actor.ID, "workspace.export", "workspace", &ws.ID, nil)
	_ = ownerQ // keep import used
	return nil
}

// adminUserExport dumps one user's content for GDPR Subject Access.
// Scope: the target user's messages + mentions + DM participations
// in the workspace. Admin-only.
func (s *Server) adminUserExport(c echo.Context) error {
	actor := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), actor.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), actor.ID, ws.ID); err != nil {
		return err
	}
	targetUID, err := parseInt64Param(c, "uid")
	if err != nil {
		return err
	}
	if s.ownerPool == nil {
		return problem.Internal("export requires the owner pool")
	}

	c.Response().Header().Set("Content-Type", "application/zip")
	c.Response().Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`,
			fmt.Sprintf("user-%d-export.zip", targetUID)))
	zw := zip.NewWriter(c.Response().Writer)
	defer zw.Close()

	// Profile
	var email, displayName string
	var emailVerifiedAt *time.Time
	err = s.ownerPool.QueryRow(c.Request().Context(),
		`SELECT email, display_name, email_verified_at FROM users WHERE id = $1`,
		targetUID).Scan(&email, &displayName, &emailVerifiedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("user not found")
		}
		return problem.Internal("load user: " + err.Error())
	}
	if err := writeJSONEntry(zw, "profile.json", map[string]any{
		"user_id":           targetUID,
		"email":             email,
		"display_name":      displayName,
		"email_verified_at": emailVerifiedAt,
		"workspace_id":      ws.ID,
	}); err != nil {
		return err
	}

	// Messages authored in the workspace.
	msgsEntry, err := zw.Create("messages.ndjson")
	if err != nil {
		return err
	}
	rows, err := s.ownerPool.Query(c.Request().Context(),
		`SELECT id, channel_id, body_md, body_blocks, created_at
		 FROM messages
		 WHERE workspace_id = $1 AND author_user_id = $2 AND deleted_at IS NULL
		 ORDER BY id`, ws.ID, targetUID)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(msgsEntry)
	for rows.Next() {
		var (
			id, channelID int64
			bodyMD        string
			bodyBlocks    []byte
			createdAt     time.Time
		)
		if err := rows.Scan(&id, &channelID, &bodyMD, &bodyBlocks, &createdAt); err != nil {
			rows.Close()
			return err
		}
		var blocks any
		if len(bodyBlocks) > 0 {
			_ = json.Unmarshal(bodyBlocks, &blocks)
		}
		_ = enc.Encode(map[string]any{
			"id":         id,
			"channel_id": channelID,
			"body_md":    bodyMD,
			"body_blocks": blocks,
			"created_at": createdAt,
		})
	}
	rows.Close()

	s.recordAudit(c, ws.ID, actor.ID, "user.export", "user", &targetUID, nil)
	return nil
}

// ---- helpers -----------------------------------------------------------

func writeJSONEntry(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	return json.NewEncoder(w).Encode(v)
}

// recordAudit fires an audit_log row. Never errors the caller's
// response — if audit write fails, the Recorder logs and continues.
func (s *Server) recordAudit(c echo.Context, workspaceID, actorID int64, action, targetKind string, targetID *int64, metadata map[string]any) {
	if s.auditor == nil {
		return
	}
	var tidStr string
	if targetID != nil {
		tidStr = fmt.Sprintf("%d", *targetID)
	}
	s.auditor.Record(c.Request().Context(), audit.Event{
		WorkspaceID: &workspaceID,
		ActorUserID: &actorID,
		ActorIP:     c.RealIP(),
		Action:      action,
		TargetKind:  targetKind,
		TargetID:    tidStr,
		Metadata:    metadata,
	})
}
