package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

func (s *Server) mountWorkspaces(api *echo.Group) {
	g := api.Group("/workspaces")
	g.Use(s.requireAuth())
	g.POST("", s.createWorkspace)
	g.GET("/:slug", s.getWorkspaceBySlug)
	g.GET("/:slug/channels", s.listWorkspaceChannels)
	g.GET("/:slug/members", s.listWorkspaceMembersHandler)
}

// ---- DTOs ----------------------------------------------------------------

type WorkspaceDTO struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	BrandColor  string    `json:"brand_color,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type WorkspaceMembershipDTO struct {
	Workspace    WorkspaceDTO      `json:"workspace"`
	Role         string            `json:"role"`
	JoinedAt     time.Time         `json:"joined_at"`
	CustomStatus json.RawMessage   `json:"custom_status,omitempty"`
	NotifyPref   string            `json:"notify_pref"`
}

type ChannelDTO struct {
	ID                int64     `json:"id"`
	WorkspaceID       int64     `json:"workspace_id"`
	Type              string    `json:"type"`
	Name              string    `json:"name,omitempty"`
	Topic             string    `json:"topic"`
	Description       string    `json:"description"`
	DefaultJoin       bool      `json:"default_join"`
	CreatedAt         time.Time `json:"created_at"`
	LastReadMessageID *int64    `json:"last_read_message_id,omitempty"`
	UnreadCount       int64     `json:"unread_count"`
	MentionCount      int64     `json:"mention_count"`
}

type WorkspaceMemberDTO struct {
	UserID          int64  `json:"user_id"`
	DisplayName     string `json:"display_name"`
	Email           string `json:"email"`
	Role            string `json:"role"`
	EmailVerifiedAt *time.Time `json:"email_verified_at,omitempty"`
}

type createWorkspaceRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

// ---- handlers ------------------------------------------------------------

// createWorkspace bootstraps a workspace: the workspace row, an owner
// membership for the creator, and a default #general channel. All three
// inserts run in one transaction so the workspace never exists without
// its owner or its first channel.
func (s *Server) createWorkspace(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}

	var req createWorkspaceRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Description = strings.TrimSpace(req.Description)

	if req.Name == "" || len(req.Name) > 64 {
		return problem.BadRequest("name must be 1-64 characters")
	}
	if err := validateSlug(req.Slug); err != nil {
		return problem.BadRequest(err.Error())
	}
	if len(req.Description) > 240 {
		return problem.BadRequest("description must be 240 characters or fewer")
	}

	// Three inserts wrapped in one tx. app.user_id is set so the workspaces
	// INSERT policy (created_by = app.user_id) accepts the row; we intentionally
	// don't set app.workspace_id yet because the workspace doesn't exist at the
	// time we INSERT it.
	var created sqlcgen.Workspace
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID}, func(scope db.TxScope) error {
		ws, err := scope.Queries.CreateWorkspace(c.Request().Context(), sqlcgen.CreateWorkspaceParams{
			Slug:        req.Slug,
			Name:        req.Name,
			Description: req.Description,
			CreatedBy:   user.ID,
		})
		if err != nil {
			return err
		}

		// First membership — owner role.
		if _, err := scope.Queries.CreateMembership(c.Request().Context(), sqlcgen.CreateMembershipParams{
			WorkspaceID: ws.ID,
			UserID:      user.ID,
			Role:        "owner",
		}); err != nil {
			return fmt.Errorf("create owner membership: %w", err)
		}

		// Default #general channel. Creation of channels is scoped by
		// app.workspace_id (RLS policy ch_all WITH CHECK), so we set it here
		// now that the workspace id is known.
		if _, err := scope.Tx.Exec(c.Request().Context(),
			"SELECT set_config('app.workspace_id', $1, true)", strconv.FormatInt(ws.ID, 10)); err != nil {
			return fmt.Errorf("set app.workspace_id: %w", err)
		}
		general := "general"
		if _, err := scope.Queries.CreateChannel(c.Request().Context(), sqlcgen.CreateChannelParams{
			WorkspaceID: ws.ID,
			Type:        "public",
			Name:        &general,
			Topic:       "Company-wide announcements and updates.",
			Description: "The default channel everyone joins.",
			DefaultJoin: true,
			CreatedBy:   &user.ID,
		}); err != nil {
			return fmt.Errorf("create default channel: %w", err)
		}

		created = ws
		return nil
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// unique_violation on workspaces.slug
			return problem.Conflict("a workspace with that slug already exists")
		}
		return problem.Internal("create workspace: " + err.Error())
	}

	s.auditor.Record(c.Request().Context(), audit.Event{
		WorkspaceID: &created.ID,
		ActorUserID: &user.ID,
		ActorIP:     clientIP(c),
		Action:      "workspace.created",
		TargetKind:  "workspace",
		TargetID:    fmt.Sprint(created.ID),
		Metadata:    map[string]any{"slug": created.Slug},
	})

	return c.JSON(http.StatusCreated, workspaceDTOFromRow(&created))
}

func (s *Server) getWorkspaceBySlug(c echo.Context) error {
	user := userFromContext(c)
	slug := strings.ToLower(c.Param("slug"))
	if slug == "" {
		return problem.BadRequest("slug required")
	}

	// Resolve slug → id in a user-scoped tx first so RLS gates that the user
	// is actually a member. A non-member gets pgx.ErrNoRows (not 403) because
	// the RLS policy hides the row entirely — this is the defense-in-depth
	// design: even enumeration via valid slug names leaks nothing.
	var out WorkspaceDTO
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, ReadOnly: true}, func(scope db.TxScope) error {
		ws, err := scope.Queries.GetWorkspaceBySlug(c.Request().Context(), slug)
		if err != nil {
			return err
		}
		out = workspaceDTOFromRow(&ws)
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("workspace not found")
		}
		return problem.Internal("load workspace: " + err.Error())
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) listWorkspaceChannels(c echo.Context) error {
	user := userFromContext(c)
	slug := strings.ToLower(c.Param("slug"))

	var channels []ChannelDTO
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, ReadOnly: true}, func(scope db.TxScope) error {
		ws, err := scope.Queries.GetWorkspaceBySlug(c.Request().Context(), slug)
		if err != nil {
			return err
		}
		if _, err := scope.Tx.Exec(c.Request().Context(),
			"SELECT set_config('app.workspace_id', $1, true)", strconv.FormatInt(ws.ID, 10)); err != nil {
			return err
		}
		rows, err := scope.Queries.ListPublicChannels(c.Request().Context())
		if err != nil {
			return err
		}

		// Hydrate per-channel membership (last_read) and unread/mention
		// counts for the current user. One query per channel is fine for
		// M4 volumes (tens of channels per workspace). If this becomes a
		// hotspot we'll fold it into a single query with lateral joins.
		memberships, err := scope.Queries.ListUserChannelMemberships(c.Request().Context(), sqlcgen.ListUserChannelMembershipsParams{
			UserID:      user.ID,
			WorkspaceID: ws.ID,
		})
		if err != nil {
			return err
		}
		memberByChannel := make(map[int64]sqlcgen.ChannelMembership, len(memberships))
		for _, m := range memberships {
			memberByChannel[m.ChannelID] = m
		}

		lowerBound := pgtype.Timestamptz{Time: time.Now().Add(-partitionPruneWindow), Valid: true}
		channels = make([]ChannelDTO, 0, len(rows))
		for _, r := range rows {
			dto := channelDTOFromRow(&r)
			if mem, ok := memberByChannel[r.ID]; ok {
				dto.LastReadMessageID = mem.LastReadMessageID
				var lastRead int64
				if mem.LastReadMessageID != nil {
					lastRead = *mem.LastReadMessageID
				}
				n, err := scope.Queries.CountMessagesAfter(c.Request().Context(), sqlcgen.CountMessagesAfterParams{
					ChannelID: r.ID,
					ID:        lastRead,
					CreatedAt: lowerBound,
				})
				if err != nil {
					return err
				}
				dto.UnreadCount = n

				mcount, err := scope.Queries.CountMentionsAfter(c.Request().Context(), sqlcgen.CountMentionsAfterParams{
					MentionedUserID: user.ID,
					ChannelID:       r.ID,
					MessageID:       lastRead,
				})
				if err != nil {
					return err
				}
				dto.MentionCount = mcount
			}
			channels = append(channels, dto)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("workspace not found")
		}
		return problem.Internal("list channels: " + err.Error())
	}
	return c.JSON(http.StatusOK, channels)
}

func (s *Server) listWorkspaceMembersHandler(c echo.Context) error {
	user := userFromContext(c)
	slug := strings.ToLower(c.Param("slug"))

	var out []WorkspaceMemberDTO
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, ReadOnly: true}, func(scope db.TxScope) error {
		ws, err := scope.Queries.GetWorkspaceBySlug(c.Request().Context(), slug)
		if err != nil {
			return err
		}
		if _, err := scope.Tx.Exec(c.Request().Context(),
			"SELECT set_config('app.workspace_id', $1, true)", strconv.FormatInt(ws.ID, 10)); err != nil {
			return err
		}
		rows, err := scope.Queries.ListWorkspaceMembers(c.Request().Context(), ws.ID)
		if err != nil {
			return err
		}
		out = make([]WorkspaceMemberDTO, 0, len(rows))
		for _, r := range rows {
			dto := WorkspaceMemberDTO{
				UserID:      r.UserID,
				DisplayName: r.DisplayName,
				Email:       r.Email,
				Role:        r.Role,
			}
			if r.EmailVerifiedAt.Valid {
				t := r.EmailVerifiedAt.Time
				dto.EmailVerifiedAt = &t
			}
			out = append(out, dto)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("workspace not found")
		}
		return problem.Internal("list members: " + err.Error())
	}
	return c.JSON(http.StatusOK, out)
}

// ---- /me/workspaces ------------------------------------------------------

func (s *Server) listMyWorkspaces(c echo.Context) error {
	user := userFromContext(c)
	if user == nil {
		return problem.Unauthorized("no user in context")
	}

	var out []WorkspaceMembershipDTO
	err := db.WithTx(c.Request().Context(), s.pool.Pool, db.TxOptions{UserID: user.ID, ReadOnly: true}, func(scope db.TxScope) error {
		rows, err := scope.Queries.ListWorkspacesForUser(c.Request().Context(), user.ID)
		if err != nil {
			return err
		}
		out = make([]WorkspaceMembershipDTO, 0, len(rows))
		for _, r := range rows {
			ws := sqlcgen.Workspace{
				ID:          r.ID,
				Slug:        r.Slug,
				Name:        r.Name,
				Description: r.Description,
				BrandColor:  r.BrandColor,
				CreatedBy:   r.CreatedBy,
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
				ArchivedAt:  r.ArchivedAt,
			}
			out = append(out, WorkspaceMembershipDTO{
				Workspace:    workspaceDTOFromRow(&ws),
				Role:         r.MembershipRole,
				JoinedAt:     r.MembershipJoinedAt.Time,
				CustomStatus: json.RawMessage(r.MembershipCustomStatus),
				NotifyPref:   r.MembershipNotifyPref,
			})
		}
		return nil
	})
	if err != nil {
		return problem.Internal("list workspaces: " + err.Error())
	}
	return c.JSON(http.StatusOK, out)
}

// ---- helpers -------------------------------------------------------------

func workspaceDTOFromRow(w *sqlcgen.Workspace) WorkspaceDTO {
	dto := WorkspaceDTO{
		ID:          w.ID,
		Slug:        w.Slug,
		Name:        w.Name,
		Description: w.Description,
		CreatedAt:   w.CreatedAt.Time,
	}
	if w.BrandColor != nil {
		dto.BrandColor = *w.BrandColor
	}
	return dto
}

func channelDTOFromRow(c *sqlcgen.Channel) ChannelDTO {
	dto := ChannelDTO{
		ID:          c.ID,
		WorkspaceID: c.WorkspaceID,
		Type:        c.Type,
		Topic:       c.Topic,
		Description: c.Description,
		DefaultJoin: c.DefaultJoin,
		CreatedAt:   c.CreatedAt.Time,
	}
	if c.Name != nil {
		dto.Name = *c.Name
	}
	return dto
}

// slugPattern mirrors RFC 3986 / URL-safe segment rules. Letters, digits,
// hyphens; must start and end alphanumeric; 2-40 chars.
var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

func validateSlug(s string) error {
	if !slugPattern.MatchString(s) {
		return errors.New("slug must be 2-40 characters, lowercase letters/digits/hyphens, starting and ending with a letter or digit")
	}
	switch s {
	case "api", "auth", "setup", "login", "signup", "admin", "me", "help", "docs", "www":
		return fmt.Errorf("slug %q is reserved", s)
	}
	return nil
}
