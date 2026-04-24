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
)

// DM endpoints (M8).
//
//   POST /workspaces/:slug/dms    — find-or-create 1:1 DM channel with `user_id`.
//   GET  /workspaces/:slug/dms    — list the current user's DMs in the workspace.
//
// Two tables drive this: `dm_pairs` is the canonical (user_a<user_b)
// uniqueness index, and `channel_memberships` hosts both ends of the
// conversation so the existing message + realtime surfaces work unchanged.

type dmDTO struct {
	ChannelID        int64     `json:"channel_id"`
	OtherUserID      int64     `json:"other_user_id"`
	OtherDisplayName string    `json:"other_display_name"`
	OtherEmail       string    `json:"other_email"`
	CreatedAt        time.Time `json:"created_at"`
}

type createDMRequest struct {
	UserID int64 `json:"user_id"`
}

func (s *Server) mountDMs(api *echo.Group) {
	g := api.Group("/workspaces/:slug/dms")
	g.Use(s.requireAuth())
	g.POST("", s.findOrCreateDM)
	g.GET("", s.listDMs)
}

// findOrCreateDM is idempotent: calling twice with the same counterpart
// returns the same channel. The UNIQUE (workspace, user_a, user_b) index
// guarantees exactly one row per pair; the ON CONFLICT in the query
// lets a duplicate call slide in cheaply. Channel + memberships are only
// created on the miss path — the hit path skips straight to returning
// the existing channel.
func (s *Server) findOrCreateDM(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var req createDMRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if req.UserID <= 0 || req.UserID == user.ID {
		return problem.BadRequest("user_id must be a different workspace member")
	}

	// Verify the target is actually a member of this workspace. Prevents
	// crafting a DM with a stranger whose id you guessed.
	if err := s.requireBothMembers(c.Request().Context(), user.ID, req.UserID, ws.ID); err != nil {
		return err
	}

	var dto dmDTO
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			// Fast path: existing pair.
			existing, err := scope.Queries.GetDMChannelForPair(c.Request().Context(), sqlcgen.GetDMChannelForPairParams{
				WorkspaceID: ws.ID,
				SelfID:      user.ID,
				OtherID:     req.UserID,
			})
			if err == nil {
				other, err := s.lookupOtherSummary(c.Request().Context(), req.UserID)
				if err != nil {
					return err
				}
				dto = dmDTO{
					ChannelID:        existing.ChannelID,
					OtherUserID:      req.UserID,
					OtherDisplayName: other.DisplayName,
					OtherEmail:       other.Email,
					CreatedAt:        existing.CreatedAt.Time,
				}
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}

			// Miss — create channel + memberships + pair.
			ch, err := scope.Queries.CreateDMChannel(c.Request().Context(), sqlcgen.CreateDMChannelParams{
				WorkspaceID: ws.ID,
				CreatedBy:   &user.ID,
			})
			if err != nil {
				return err
			}
			// Both participants join the channel so existing messaging
			// code (RLS + realtime subscribe) treats them as members.
			for _, uid := range []int64{user.ID, req.UserID} {
				if _, err := scope.Queries.CreateChannelMembership(c.Request().Context(), sqlcgen.CreateChannelMembershipParams{
					WorkspaceID: ws.ID,
					ChannelID:   ch.ID,
					UserID:      uid,
				}); err != nil {
					return err
				}
			}
			pair, err := scope.Queries.FindOrCreateDMChannel(c.Request().Context(), sqlcgen.FindOrCreateDMChannelParams{
				WorkspaceID: ws.ID,
				SelfID:      user.ID,
				OtherID:     req.UserID,
				ChannelID:   ch.ID,
			})
			if err != nil {
				return err
			}
			other, err := s.lookupOtherSummary(c.Request().Context(), req.UserID)
			if err != nil {
				return err
			}
			dto = dmDTO{
				ChannelID:        pair.ChannelID,
				OtherUserID:      req.UserID,
				OtherDisplayName: other.DisplayName,
				OtherEmail:       other.Email,
				CreatedAt:        pair.CreatedAt.Time,
			}
			return nil
		})
	if err != nil {
		return problem.Internal("find-or-create dm: " + err.Error())
	}
	return c.JSON(http.StatusOK, dto)
}

func (s *Server) listDMs(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}
	slug := c.Param("slug")

	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}

	var rows []sqlcgen.ListDMChannelsForUserRow
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListDMChannelsForUser(c.Request().Context(), sqlcgen.ListDMChannelsForUserParams{
				WorkspaceID: ws.ID,
				UserID:      user.ID,
			})
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list dms: " + err.Error())
	}

	out := make([]dmDTO, 0, len(rows))
	for _, r := range rows {
		dto := dmDTO{
			ChannelID:        r.ChannelID,
			OtherUserID:      r.OtherUserID,
			OtherDisplayName: r.OtherDisplayName,
			OtherEmail:       r.OtherEmail,
			CreatedAt:        r.ChannelCreatedAt.Time,
		}
		out = append(out, dto)
	}
	return c.JSON(http.StatusOK, out)
}

// ---- helpers -----------------------------------------------------------

// requireBothMembers confirms the target user is also an active member of
// the workspace. Runs on the owner pool with workspace_memberships RLS
// disabled so we don't need a round-trip through each user's GUC.
type otherSummary struct {
	DisplayName string
	Email       string
}

func (s *Server) requireBothMembers(ctx context.Context, selfID, otherID, workspaceID int64) error {
	// Self was already confirmed when resolveWorkspaceBySlug found the
	// workspace under their RLS scope. We just need to verify the other.
	var role string
	err := db.WithTx(ctx, s.pool.Pool, db.TxOptions{UserID: selfID, WorkspaceID: workspaceID, ReadOnly: true}, func(scope db.TxScope) error {
		m, err := scope.Queries.GetMembershipForUser(ctx, sqlcgen.GetMembershipForUserParams{
			WorkspaceID: workspaceID,
			UserID:      otherID,
		})
		if err != nil {
			return err
		}
		role = m.Role
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("other user is not in this workspace")
		}
		return problem.Internal("check other membership: " + err.Error())
	}
	_ = role
	return nil
}

// lookupOtherSummary returns the display name + email of the other DM
// participant. Users table is global (no RLS), so we can look up by id
// directly without threading the workspace scope.
func (s *Server) lookupOtherSummary(ctx context.Context, userID int64) (*otherSummary, error) {
	u, err := s.queries.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &otherSummary{DisplayName: u.DisplayName, Email: u.Email}, nil
}
