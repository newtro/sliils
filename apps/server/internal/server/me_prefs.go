package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Per-workspace membership preferences (M7B + M7C):
//
//   PATCH /me/workspaces/:slug/status        — set/clear custom_status JSONB
//   PATCH /me/workspaces/:slug/notify-pref   — set workspace-default
//                                              notification preference
//
// Both endpoints update only the current user's row (never someone else's)
// and rely on wsm_modify's user_id clause so they succeed without needing
// app.workspace_id set. That simplifies the call site — no resolve-slug
// round trip required just to stamp your own status.

// mountMePrefs is wired from mountMe so the Echo group already carries
// requireAuth().
func (s *Server) mountMePrefs(g *echo.Group) {
	g.PATCH("/workspaces/:slug/status", s.updateMyStatus)
	g.PATCH("/workspaces/:slug/notify-pref", s.updateMyNotifyPref)
}

// ---- custom status ------------------------------------------------------

type updateStatusRequest struct {
	// Emoji + Text are the canonical shape. Absent/empty values clear the
	// status by writing `{}` JSONB.
	Emoji     string          `json:"emoji,omitempty"`
	Text      string          `json:"text,omitempty"`
	ExpiresAt *string         `json:"expires_at,omitempty"`
	// Extra lets clients include forward-compatible fields the server
	// doesn't know about yet. The server persists verbatim after
	// re-marshaling to drop unknown top-level fields (see below).
	Extra map[string]any `json:"extra,omitempty"`
}

func (s *Server) updateMyStatus(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var req updateStatusRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	// Build the status JSONB. If every meaningful field is empty, we clear
	// by writing '{}' so the UI can tell "no status set" apart from
	// "status with empty text".
	obj := map[string]any{}
	if req.Emoji != "" {
		obj["emoji"] = req.Emoji
	}
	if req.Text != "" {
		if len(req.Text) > 140 {
			return problem.BadRequest("status text must be 140 characters or fewer")
		}
		obj["text"] = req.Text
	}
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		obj["expires_at"] = *req.ExpiresAt
	}
	for k, v := range req.Extra {
		obj[k] = v
	}

	payload, err := json.Marshal(obj)
	if err != nil {
		return problem.Internal("marshal status: " + err.Error())
	}

	var updated sqlcgen.WorkspaceMembership
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			m, err := scope.Queries.UpdateMembershipCustomStatus(c.Request().Context(), sqlcgen.UpdateMembershipCustomStatusParams{
				WorkspaceID: ws.ID,
				UserID:      user.ID,
				Column3:     payload,
			})
			if err != nil {
				return err
			}
			updated = m
			return nil
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.Forbidden("not a member of this workspace")
		}
		return problem.Internal("update status: " + err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{
		"workspace_id":  ws.ID,
		"custom_status": json.RawMessage(updated.CustomStatus),
	})
}

// ---- workspace-level notification preference ----------------------------

type updateNotifyPrefRequest struct {
	NotifyPref string `json:"notify_pref"`
}

func (s *Server) updateMyNotifyPref(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var req updateNotifyPrefRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	switch req.NotifyPref {
	case "all", "mentions", "mute":
		// valid
	default:
		return problem.BadRequest("notify_pref must be one of all|mentions|mute")
	}

	var updated sqlcgen.WorkspaceMembership
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			m, err := scope.Queries.UpdateMembershipNotifyPref(c.Request().Context(), sqlcgen.UpdateMembershipNotifyPrefParams{
				WorkspaceID: ws.ID,
				UserID:      user.ID,
				NotifyPref:  req.NotifyPref,
			})
			if err != nil {
				return err
			}
			updated = m
			return nil
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.Forbidden("not a member of this workspace")
		}
		return problem.Internal("update notify_pref: " + err.Error())
	}

	return c.JSON(http.StatusOK, map[string]any{
		"workspace_id": ws.ID,
		"notify_pref":  updated.NotifyPref,
	})
}
