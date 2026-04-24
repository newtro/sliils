package server

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Channel creation (M12-polish).
//
// Only workspace owners + admins can create channels at v1. The creator
// is automatically added as a member (so the sidebar lights up for
// them without a second round trip). v1 supports public channels only;
// private channels arrive in v1.1 alongside per-channel ACLs.

type createChannelRequest struct {
	Name        string `json:"name"`
	Topic       string `json:"topic,omitempty"`
	Description string `json:"description,omitempty"`
	// Type is always "public" at v1 — accept it in the body anyway so
	// the UI doesn't have to special-case when private support lands.
	Type        string `json:"type,omitempty"`
}

// channelNamePattern: lowercase alphanumerics + hyphen, 1..80 chars.
// Matches Slack's channel-name rules loosely. Rejected via 400 with a
// descriptive message so UIs can surface what went wrong.
var channelNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,79}$`)

func (s *Server) mountChannels(api *echo.Group) {
	g := api.Group("")
	g.Use(s.requireAuth())
	g.POST("/workspaces/:slug/channels", s.createChannel)
}

func (s *Server) createChannel(c echo.Context) error {
	user := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}

	var req createChannelRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	if !channelNamePattern.MatchString(name) {
		return problem.BadRequest("name must be 1-80 lowercase letters, digits, or hyphens")
	}
	typ := strings.TrimSpace(req.Type)
	if typ == "" {
		typ = "public"
	}
	if typ != "public" {
		return problem.BadRequest("only public channels are supported at v1")
	}

	var created sqlcgen.Channel
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			// Uniqueness: the schema has a partial UNIQUE (workspace, name)
			// on named channels, so a duplicate name 500s with a unique-
			// constraint error. Map it to a clean 409.
			var nameVal *string
			if name != "" {
				n := name
				nameVal = &n
			}
			ch, err := scope.Queries.CreateChannel(c.Request().Context(), sqlcgen.CreateChannelParams{
				WorkspaceID: ws.ID,
				Type:        typ,
				Name:        nameVal,
				Topic:       strings.TrimSpace(req.Topic),
				Description: strings.TrimSpace(req.Description),
				DefaultJoin: false,
				CreatedBy:   &user.ID,
			})
			if err != nil {
				return err
			}
			created = ch
			// Creator auto-joins so the sidebar shows the channel.
			if _, err := scope.Queries.CreateChannelMembership(c.Request().Context(), sqlcgen.CreateChannelMembershipParams{
				WorkspaceID: ws.ID,
				ChannelID:   ch.ID,
				UserID:      user.ID,
				Column4:     "all",
			}); err != nil {
				return err
			}
			return nil
		})
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return problem.Conflict("a channel with that name already exists in this workspace")
		}
		return problem.Internal("create channel: " + err.Error())
	}

	dto := ChannelDTO{
		ID:          created.ID,
		WorkspaceID: created.WorkspaceID,
		Type:        created.Type,
		Topic:       created.Topic,
		Description: created.Description,
		DefaultJoin: created.DefaultJoin,
		CreatedAt:   created.CreatedAt.Time,
		// Creator just joined, so no unread — but zero values are fine.
	}
	if created.Name != nil {
		dto.Name = *created.Name
	}
	return c.JSON(http.StatusCreated, dto)
}
