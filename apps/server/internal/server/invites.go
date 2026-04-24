package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Invite token config. 32 bytes of randomness → 256 bits of entropy, base64
// URL-safe so it slots cleanly into `/invite/:token` routes without escapes.
const (
	inviteTokenBytes = 32
	inviteTTL        = 7 * 24 * time.Hour
)

// ---- DTOs ---------------------------------------------------------------

type InviteDTO struct {
	ID                  int64     `json:"id"`
	WorkspaceID         int64     `json:"workspace_id"`
	Token               string    `json:"token,omitempty"`  // only returned to the admin who created it
	Email               string    `json:"email,omitempty"`  // empty for link-only invites
	Role                string    `json:"role"`
	CreatedAt           time.Time `json:"created_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	CreatorDisplayName  string    `json:"creator_display_name,omitempty"`
	CreatorEmail        string    `json:"creator_email,omitempty"`
}

// InvitePreviewDTO is what an unauthenticated visitor sees at /invite/:token
// — enough to recognize the workspace and inviter, nothing more. Notably
// NOT included: the token itself (already known), creator email.
type InvitePreviewDTO struct {
	WorkspaceSlug        string    `json:"workspace_slug"`
	WorkspaceName        string    `json:"workspace_name"`
	WorkspaceDescription string    `json:"workspace_description,omitempty"`
	Email                string    `json:"email,omitempty"` // if email-targeted, surface so the client can prefill signup
	Role                 string    `json:"role"`
	ExpiresAt            time.Time `json:"expires_at"`
	// Status flags for the UI.
	Accepted bool `json:"accepted"`
	Revoked  bool `json:"revoked"`
	Expired  bool `json:"expired"`
}

type createInviteRequest struct {
	Email string `json:"email,omitempty"` // optional — link-only invites allowed
	Role  string `json:"role,omitempty"`  // admin | member | guest; defaults to member
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountInvites(api *echo.Group) {
	// Admin-only operations — scoped under /workspaces/:slug/invites.
	admin := api.Group("/workspaces/:slug/invites")
	admin.Use(s.requireAuth())
	admin.POST("", s.createInvite)
	admin.GET("", s.listInvites)
	admin.DELETE("/:id", s.revokeInvite)

	// Token-based preview / accept — intentionally NOT under a workspace
	// slug because the user doesn't know the workspace yet. Preview is
	// unauthenticated; accept requires auth.
	api.GET("/invites/:token", s.previewInvite)
	api.POST("/invites/:token/accept", s.acceptInvite, s.requireAuth())
}

// ---- handlers: admin create / list / revoke ------------------------------

func (s *Server) createInvite(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}

	var req createInviteRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Role = strings.TrimSpace(req.Role)
	if req.Role == "" {
		req.Role = "member"
	}
	if !isValidInviteRole(req.Role) {
		return problem.BadRequest("role must be one of admin|member|guest")
	}
	if req.Email != "" && !looksLikeEmail(req.Email) {
		return problem.BadRequest("email is not valid")
	}

	token, err := newInviteToken()
	if err != nil {
		return problem.Internal("generate token: " + err.Error())
	}

	expires := pgtype.Timestamptz{Time: time.Now().Add(inviteTTL), Valid: true}

	var row sqlcgen.WorkspaceInvite
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			var emailPtr *string
			if req.Email != "" {
				e := req.Email
				emailPtr = &e
			}
			r, err := scope.Queries.CreateWorkspaceInvite(c.Request().Context(), sqlcgen.CreateWorkspaceInviteParams{
				WorkspaceID: ws.ID,
				Token:       token,
				Email:       emailPtr,
				Column4:     req.Role,
				CreatedBy:   user.ID,
				ExpiresAt:   expires,
			})
			if err != nil {
				return err
			}
			row = r
			return nil
		})
	if err != nil {
		return problem.Internal("create invite: " + err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		WorkspaceID: &ws.ID,
		Action:      "workspace.invite.created",
		TargetKind:  "invite",
		TargetID:    strconv.FormatInt(row.ID, 10),
		Metadata: map[string]any{
			"email": req.Email,
			"role":  req.Role,
		},
	})

	acceptURL := fmt.Sprintf("%s/invite/%s", s.cfg.PublicBaseURL, token)

	// Fire the email best-effort — the DB row is the source of truth, so an
	// email failure shouldn't block the invite. Admins get the token back
	// on the API response and can share it manually.
	if req.Email != "" && s.email != nil {
		go func(recipient, wsName, inviterName, url string) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			msg := email.WorkspaceInvite(recipient, wsName, inviterName, url)
			if err := s.email.Send(ctx, msg); err != nil {
				s.logger.Warn("invite email send failed",
					"error", err.Error(),
					"workspace_id", ws.ID,
					"recipient", recipient,
				)
			}
		}(req.Email, ws.Name, user.DisplayName, acceptURL)
	}

	dto := inviteDTO(sqlcgen.ListPendingInvitesForWorkspaceRow{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		Token:       row.Token,
		Email:       row.Email,
		Role:        row.Role,
		CreatedBy:   row.CreatedBy,
		CreatedAt:   row.CreatedAt,
		ExpiresAt:   row.ExpiresAt,
		CreatorDisplayName: &user.DisplayName,
		CreatorEmail:       &user.Email,
	}, true)
	dto.Token = token // admin caller sees the token so they can copy-paste if needed
	return c.JSON(http.StatusCreated, dto)
}

func (s *Server) listInvites(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}

	var rows []sqlcgen.ListPendingInvitesForWorkspaceRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListPendingInvitesForWorkspace(c.Request().Context(), ws.ID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list invites: " + err.Error())
	}

	out := make([]InviteDTO, 0, len(rows))
	for _, r := range rows {
		// Admins see the tokens for pending invites so they can re-share by
		// copying a link without going through email.
		out = append(out, inviteDTO(r, true))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) revokeInvite(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")
	inviteID, err := parsePathInt64(c, "id")
	if err != nil {
		return problem.BadRequest("invalid invite id")
	}

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}

	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			return scope.Queries.RevokeInvite(c.Request().Context(), sqlcgen.RevokeInviteParams{
				ID:        inviteID,
				RevokedBy: &user.ID,
			})
		})
	if err != nil {
		return problem.Internal("revoke invite: " + err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		WorkspaceID: &ws.ID,
		Action:      "workspace.invite.revoked",
		TargetKind:  "invite",
		TargetID:    strconv.FormatInt(inviteID, 10),
	})

	return c.NoContent(http.StatusNoContent)
}

// ---- handlers: unauthenticated preview / authenticated accept -----------

// previewInvite is how the web app renders /invite/:token without forcing
// login first — the page shows "You've been invited to Foo" even when the
// visitor has no session. Lookup runs on the owner pool so RLS doesn't
// hide the row. The response intentionally leaks only what the email
// already revealed (workspace name, invited email) plus status flags.
func (s *Server) previewInvite(c echo.Context) error {
	token := c.Param("token")
	if token == "" {
		return problem.BadRequest("missing token")
	}
	if s.ownerPool == nil {
		return problem.ServiceUnavailable("invite preview requires the privileged database pool")
	}

	q := sqlcgen.New(s.ownerPool)
	row, err := q.GetInviteByToken(c.Request().Context(), token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("invite not found")
		}
		return problem.Internal("preview invite: " + err.Error())
	}

	now := time.Now()
	preview := InvitePreviewDTO{
		WorkspaceSlug: row.WorkspaceSlug,
		WorkspaceName: row.WorkspaceName,
		Role:          row.Role,
		ExpiresAt:     row.ExpiresAt.Time,
		Accepted:      row.AcceptedAt.Valid,
		Revoked:       row.RevokedAt.Valid,
		Expired:       row.ExpiresAt.Valid && now.After(row.ExpiresAt.Time),
	}
	if row.Email != nil {
		preview.Email = *row.Email
	}
	if row.WorkspaceDescription != "" {
		preview.WorkspaceDescription = row.WorkspaceDescription
	}
	return c.JSON(http.StatusOK, preview)
}

// acceptInvite enrolls the current user into the inviting workspace. Runs on
// the owner pool for the token lookup + the membership insert — avoids the
// chicken-and-egg where the user isn't yet in the workspace_memberships
// table (so RLS on workspaces would hide it from a tenant-pool query).
//
// Safety checks we enforce here (all server-side, never trust the client):
//   - invite exists, not accepted, not revoked, not expired
//   - if the invite was email-targeted, the accepting user's email matches
//   - membership upsert re-activates a soft-deactivated row rather than
//     creating a duplicate
func (s *Server) acceptInvite(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	token := c.Param("token")
	if token == "" {
		return problem.BadRequest("missing token")
	}
	if s.ownerPool == nil {
		return problem.ServiceUnavailable("invite accept requires the privileged database pool")
	}

	q := sqlcgen.New(s.ownerPool)
	row, err := q.GetInviteByToken(c.Request().Context(), token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("invite not found")
		}
		return problem.Internal("lookup invite: " + err.Error())
	}
	if row.AcceptedAt.Valid {
		return problem.Conflict("invite already accepted")
	}
	if row.RevokedAt.Valid {
		return problem.Conflict("invite was revoked")
	}
	if row.ExpiresAt.Valid && time.Now().After(row.ExpiresAt.Time) {
		return problem.Conflict("invite expired")
	}
	if row.Email != nil && !strings.EqualFold(*row.Email, user.Email) {
		// Email-targeted invite but caller's address doesn't match. Treat
		// as forbidden rather than not-found so a legitimate visitor knows
		// they need to sign in with the right account.
		return problem.Forbidden("this invite is for a different email address")
	}

	if _, err := q.InsertWorkspaceMembershipFromInvite(c.Request().Context(), sqlcgen.InsertWorkspaceMembershipFromInviteParams{
		WorkspaceID: row.WorkspaceID,
		UserID:      user.ID,
		Role:        row.Role,
	}); err != nil {
		return problem.Internal("enroll membership: " + err.Error())
	}

	if err := q.MarkInviteAccepted(c.Request().Context(), sqlcgen.MarkInviteAcceptedParams{
		ID:         row.ID,
		AcceptedBy: &user.ID,
	}); err != nil {
		// Membership is already created; this is cosmetic. Log and continue.
		s.logger.Warn("mark invite accepted failed", "error", err.Error(), "invite_id", row.ID)
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		ActorUserID: &user.ID,
		WorkspaceID: &row.WorkspaceID,
		Action:      "workspace.invite.accepted",
		TargetKind:  "invite",
		TargetID:    strconv.FormatInt(row.ID, 10),
		Metadata: map[string]any{
			"role": row.Role,
		},
	})

	return c.JSON(http.StatusOK, map[string]any{
		"workspace_slug": row.WorkspaceSlug,
		"workspace_name": row.WorkspaceName,
		"role":           row.Role,
	})
}

// ---- helpers -----------------------------------------------------------

// resolveWorkspaceBySlug loads a workspace row under the current user's
// RLS scope. The workspaces RLS policy (SELECT via membership) requires
// `app.user_id` to be set, so we run inside a read-only tx with UserID
// plumbed. Returns 404 if the user isn't a member, matching the existing
// pattern — the client can't tell "doesn't exist" from "not a member".
func (s *Server) resolveWorkspaceBySlug(ctx context.Context, userID int64, slug string) (*sqlcgen.Workspace, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return nil, problem.BadRequest("slug is required")
	}
	var out sqlcgen.Workspace
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, ReadOnly: true}, func(scope db.TxScope) error {
		w, err := scope.Queries.GetWorkspaceBySlug(ctx, slug)
		if err != nil {
			return err
		}
		out = w
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, problem.NotFound("workspace not found")
		}
		return nil, problem.Internal("resolve workspace: " + err.Error())
	}
	return &out, nil
}

// requireWorkspaceAdmin gates admin-only operations. 403 rather than 404
// because the user is clearly at least a member (they resolved the slug).
func (s *Server) requireWorkspaceAdmin(ctx context.Context, userID, workspaceID int64) error {
	var role string
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: workspaceID, ReadOnly: true}, func(scope db.TxScope) error {
		m, err := scope.Queries.GetMembershipForUser(ctx, sqlcgen.GetMembershipForUserParams{
			WorkspaceID: workspaceID,
			UserID:      userID,
		})
		if err != nil {
			return err
		}
		role = m.Role
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.Forbidden("not a member of this workspace")
		}
		return problem.Internal("check membership: " + err.Error())
	}
	if role != "owner" && role != "admin" {
		return problem.Forbidden("admin role required")
	}
	return nil
}

func inviteDTO(r sqlcgen.ListPendingInvitesForWorkspaceRow, includeToken bool) InviteDTO {
	out := InviteDTO{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		Role:        r.Role,
		CreatedAt:   r.CreatedAt.Time,
		ExpiresAt:   r.ExpiresAt.Time,
	}
	if includeToken {
		out.Token = r.Token
	}
	if r.Email != nil {
		out.Email = *r.Email
	}
	if r.CreatorDisplayName != nil {
		out.CreatorDisplayName = *r.CreatorDisplayName
	}
	if r.CreatorEmail != nil {
		out.CreatorEmail = *r.CreatorEmail
	}
	return out
}

// newInviteToken returns a URL-safe base64 string with 256 bits of
// entropy. Uses crypto/rand, no seeding, panics only on system-level
// failure.
func newInviteToken() (string, error) {
	b := make([]byte, inviteTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func isValidInviteRole(role string) bool {
	switch role {
	case "admin", "member", "guest":
		return true
	default:
		return false
	}
}

// looksLikeEmail is a cheap format check. The real validation happens when
// the invited user signs up / accepts — we're just rejecting obvious typos.
func looksLikeEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return !strings.ContainsAny(s, " \t\r\n")
}
