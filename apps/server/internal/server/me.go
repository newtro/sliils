package server

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// UserDTO is the shape returned by any endpoint that serializes a user.
// Secrets (password_hash, totp_secret) are never serialized.
type UserDTO struct {
	ID              int64      `json:"id"`
	Email           string     `json:"email"`
	DisplayName     string     `json:"display_name"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	NeedsSetup      bool       `json:"needs_setup"`
}

type updateMeRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
}

func (s *Server) mountMe(api *echo.Group) {
	g := api.Group("/me")
	g.Use(s.requireAuth())
	g.GET("", s.getMe)
	g.PATCH("", s.updateMe)
	g.GET("/workspaces", s.listMyWorkspaces)
	s.mountMePrefs(g)
}

func (s *Server) getMe(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}

	dto := userFromRow(user)

	// Needs-setup flag: true iff the user has zero active workspace memberships.
	// Drives the client-side redirect to /setup after signup / first login.
	var count int64
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, ReadOnly: true}, func(scope db.TxScope) error {
		n, err := scope.Queries.CountWorkspacesForUser(c.Request().Context(), user.ID)
		if err != nil {
			return err
		}
		count = n
		return nil
	})
	if err != nil {
		return problem.Internal("count workspaces: " + err.Error())
	}
	dto.NeedsSetup = count == 0

	return c.JSON(http.StatusOK, dto)
}

func (s *Server) updateMe(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}

	var req updateMeRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	if req.DisplayName != nil {
		name := *req.DisplayName
		if len(name) > 64 {
			return problem.BadRequest("display_name must be 64 characters or fewer")
		}
		if err := s.queries.UpdateUserDisplayName(c.Request().Context(), sqlcgen.UpdateUserDisplayNameParams{
			ID:          user.ID,
			DisplayName: name,
		}); err != nil {
			return problem.Internal("update display name: " + err.Error())
		}
		user.DisplayName = name
	}

	return c.JSON(http.StatusOK, userFromRow(user))
}
