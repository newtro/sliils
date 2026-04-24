package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/search"
)

// searchRequest is the JSON body of POST /search.
type searchRequest struct {
	WorkspaceID int64  `json:"workspace_id"`
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
	Offset      int    `json:"offset,omitempty"`

	// If true, the response will include a fresh tenant token so the client
	// can make subsequent direct Meilisearch calls without round-tripping
	// through the server. Clients that only ever use POST /search can omit
	// this; saves a few ms per search.
	IssueToken bool `json:"issue_token,omitempty"`
}

// searchResponse wraps the core result with an optional tenant token.
type searchResponse struct {
	*search.SearchResult
	Token *search.TenantToken `json:"token,omitempty"`
}

func (s *Server) mountSearch(api *echo.Group) {
	if s.search == nil {
		// Search is disabled at this install — the endpoint returns 503 so
		// the client surfaces the misconfiguration clearly instead of going
		// silent.
		api.POST("/search", s.searchDisabled)
		return
	}
	g := api.Group("")
	g.Use(s.requireAuth())
	g.POST("/search", s.doSearch)
}

func (s *Server) searchDisabled(c echo.Context) error {
	return problem.ServiceUnavailable("search is not enabled on this install")
}

// doSearch is the main search handler. Cross-workspace safety lives inside
// search.Service.Search — this method's job is request validation and a
// membership pre-check against workspace_id.
func (s *Server) doSearch(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}

	var req searchRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if req.WorkspaceID <= 0 {
		return problem.BadRequest("workspace_id is required")
	}

	// Confirm the caller actually belongs to the workspace they're claiming.
	// Without this check a crafted request could ask for workspace B's index
	// and — even though the filter would return zero hits — we'd still spend
	// cycles hitting Meili. Fast-fail with 403 for clarity.
	ctx := c.Request().Context()
	if err := s.requireWorkspaceMembership(ctx, user.ID, req.WorkspaceID); err != nil {
		return err
	}
	result, err := s.search.Search(ctx, search.SearchParams{
		WorkspaceID: req.WorkspaceID,
		UserID:      user.ID,
		RawQuery:    req.Query,
		Limit:       req.Limit,
		Offset:      req.Offset,
	})
	if err != nil {
		return problem.Internal("search: " + err.Error())
	}

	resp := searchResponse{SearchResult: result}
	if req.IssueToken && s.search.Tokens() != nil {
		ttl := s.cfg.SearchTokenTTL
		if ttl <= 0 {
			ttl = 15 * time.Minute
		}
		token, err := s.search.IssueToken(req.WorkspaceID, user.ID, ttl)
		if err != nil {
			// Tokens are advisory — search succeeded. Log and omit the token.
			s.logger.Warn("issue tenant token failed", "error", err.Error())
		} else {
			resp.Token = token
		}
	}

	return c.JSON(http.StatusOK, resp)
}

// requireWorkspaceMembership blocks callers who aren't a member of the
// workspace they're targeting. Uses the runtime pool so the RLS policies
// on workspace_memberships apply (via WithTx setting app.user_id).
func (s *Server) requireWorkspaceMembership(ctx context.Context, userID, workspaceID int64) error {
	var found bool
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: userID, WorkspaceID: workspaceID, ReadOnly: true}, func(scope db.TxScope) error {
		_, err := scope.Queries.GetMembershipForUser(ctx, sqlcgen.GetMembershipForUserParams{
			WorkspaceID: workspaceID,
			UserID:      userID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return problem.Internal("membership check: " + err.Error())
	}
	if !found {
		return problem.Forbidden("not a member of workspace")
	}
	return nil
}
