package server

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

type ctxKey string

const (
	ctxUser      ctxKey = "sliils.user"
	ctxSessionID ctxKey = "sliils.session_id"
)

// requireAuth validates the Bearer JWT and loads the user row. On success,
// the user record and session id are placed on the request context so
// handlers can read them via userFromContext / sessionIDFromContext.
func (s *Server) requireAuth() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authz := c.Request().Header.Get(echo.HeaderAuthorization)
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				return problem.Unauthorized("missing bearer token")
			}
			raw := strings.TrimPrefix(authz, prefix)

			claims, err := s.tokens.Parse(raw)
			if err != nil {
				return problem.Unauthorized("invalid or expired access token")
			}

			if s.queries == nil {
				return problem.Internal("database not configured")
			}

			user, err := s.queries.GetUserByID(c.Request().Context(), claims.UserID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return problem.Unauthorized("user not found")
				}
				return problem.Internal("load user: " + err.Error())
			}

			ctx := c.Request().Context()
			ctx = context.WithValue(ctx, ctxUser, &user)
			ctx = context.WithValue(ctx, ctxSessionID, claims.SessionID)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

func userFromContext(c echo.Context) *sqlcgen.User {
	v, _ := c.Request().Context().Value(ctxUser).(*sqlcgen.User)
	return v
}

// requireSuperAdmin chains after requireAuth and rejects non-super-admins.
// Used by install-level endpoints (signup mode, infrastructure config,
// install-wide email defaults). Must ALWAYS be mounted under requireAuth.
func (s *Server) requireSuperAdmin() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			user := userFromContext(c)
			if user == nil {
				return problem.Unauthorized("no user in context")
			}
			if !user.IsSuperAdmin {
				return problem.Forbidden("super-admin access required")
			}
			return next(c)
		}
	}
}

func sessionIDFromContext(c echo.Context) int64 {
	v, _ := c.Request().Context().Value(ctxSessionID).(int64)
	return v
}

// clientIP returns the client's IP address without any trailing port suffix.
func clientIP(c echo.Context) string {
	ip := c.RealIP()
	if i := strings.LastIndexByte(ip, ':'); i > 0 && !strings.Contains(ip[:i], ":") {
		return ip[:i]
	}
	return ip
}
