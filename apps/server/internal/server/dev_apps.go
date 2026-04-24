package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/apps"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Developer portal (M12-P1).
//
// These endpoints are what a developer uses to create + manage an app:
//
//   GET    /dev/apps              — list my apps
//   POST   /dev/apps              — create a new app
//   GET    /dev/apps/:slug        — app detail
//   PATCH  /dev/apps/:slug        — update manifest / name / description
//   POST   /dev/apps/:slug/rotate-secret   — rotate client_secret (returns new secret ONCE)
//   DELETE /dev/apps/:slug        — soft delete
//
// Apps live at the install-global level: creation is scoped by the
// developer's own user id, not by workspace. Install into a workspace
// is a separate flow (see oauth.go).

// ---- DTOs --------------------------------------------------------------

type AppDTO struct {
	ID          int64             `json:"id"`
	Slug        string            `json:"slug"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	ClientID    string            `json:"client_id"`
	Manifest    *apps.Manifest    `json:"manifest"`
	OwnerUserID int64             `json:"owner_user_id"`
	IsPublic    bool              `json:"is_public"`
	CreatedAt   time.Time         `json:"created_at"`
}

type createAppRequest struct {
	Slug        string         `json:"slug"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Manifest    *apps.Manifest `json:"manifest,omitempty"`
}

// createAppResponse shows the client_secret ONCE.
type createAppResponse struct {
	App          AppDTO `json:"app"`
	ClientSecret string `json:"client_secret"` // show once, never returned again
}

type patchAppRequest struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	Manifest    *apps.Manifest `json:"manifest,omitempty"`
}

// ---- routes -------------------------------------------------------------

func (s *Server) mountDevApps(api *echo.Group) {
	g := api.Group("/dev/apps")
	g.Use(s.requireAuth())
	g.GET("", s.listMyApps)
	g.POST("", s.createDevApp)
	g.GET("/:slug", s.getDevApp)
	g.PATCH("/:slug", s.patchDevApp)
	g.POST("/:slug/rotate-secret", s.rotateDevAppSecret)
	g.DELETE("/:slug", s.deleteDevApp)
}

// ---- list + get --------------------------------------------------------

func (s *Server) listMyApps(c echo.Context) error {
	user := userFromContext(c)
	if s.ownerPool == nil {
		return problem.Internal("dev apps require the owner pool")
	}
	q := sqlcgen.New(s.ownerPool)
	rows, err := q.ListAppsForOwner(c.Request().Context(), user.ID)
	if err != nil {
		return problem.Internal("list apps: " + err.Error())
	}
	out := make([]AppDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, appToDTO(&r))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) getDevApp(c echo.Context) error {
	user := userFromContext(c)
	app, err := s.loadOwnedApp(c, user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, appToDTO(app))
}

// ---- create ------------------------------------------------------------

func (s *Server) createDevApp(c echo.Context) error {
	user := userFromContext(c)
	if s.ownerPool == nil {
		return problem.Internal("dev apps require the owner pool")
	}

	var req createAppRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	req.Name = strings.TrimSpace(req.Name)
	if !validSlug(req.Slug) {
		return problem.BadRequest("slug must be lowercase letters, digits, and hyphens (3..64 chars)")
	}
	if req.Name == "" {
		return problem.BadRequest("name is required")
	}

	manifest := req.Manifest
	if manifest == nil {
		manifest = &apps.Manifest{}
	}
	if err := manifest.Validate(); err != nil {
		return problem.BadRequest(err.Error())
	}
	manifestJSON, _ := json.Marshal(manifest)

	clientID, err := apps.NewClientID()
	if err != nil {
		return problem.Internal("mint client id: " + err.Error())
	}
	plainSecret, secretHash, err := apps.NewClientSecret()
	if err != nil {
		return problem.Internal("mint client secret: " + err.Error())
	}

	q := sqlcgen.New(s.ownerPool)
	app, err := q.CreateApp(c.Request().Context(), sqlcgen.CreateAppParams{
		Slug:             req.Slug,
		Name:             req.Name,
		Description:      req.Description,
		OwnerUserID:      user.ID,
		AvatarFileID:     nil,
		Manifest:         manifestJSON,
		ClientID:         clientID,
		ClientSecretHash: secretHash,
	})
	if err != nil {
		// slug uniqueness is the likely failure mode
		if strings.Contains(err.Error(), "unique") {
			return problem.Conflict("an app with that slug already exists")
		}
		return problem.Internal("create app: " + err.Error())
	}
	return c.JSON(http.StatusCreated, createAppResponse{
		App:          appToDTO(&app),
		ClientSecret: plainSecret,
	})
}

// ---- patch -------------------------------------------------------------

func (s *Server) patchDevApp(c echo.Context) error {
	user := userFromContext(c)
	app, err := s.loadOwnedApp(c, user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	var req patchAppRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	// Start from the existing manifest and layer in the patch.
	current, err := apps.DecodeManifest(app.Manifest)
	if err != nil {
		return problem.Internal("decode current manifest: " + err.Error())
	}
	if req.Manifest != nil {
		if err := req.Manifest.Validate(); err != nil {
			return problem.BadRequest(err.Error())
		}
		current = req.Manifest
	}
	manifestJSON, _ := json.Marshal(current)

	q := sqlcgen.New(s.ownerPool)
	updated, err := q.UpdateAppManifest(c.Request().Context(), sqlcgen.UpdateAppManifestParams{
		ID:          app.ID,
		Manifest:    manifestJSON,
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		return problem.Internal("update app: " + err.Error())
	}
	return c.JSON(http.StatusOK, appToDTO(&updated))
}

// ---- rotate secret / delete --------------------------------------------

func (s *Server) rotateDevAppSecret(c echo.Context) error {
	user := userFromContext(c)
	app, err := s.loadOwnedApp(c, user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	plain, hash, err := apps.NewClientSecret()
	if err != nil {
		return problem.Internal("mint secret: " + err.Error())
	}
	q := sqlcgen.New(s.ownerPool)
	if err := q.RotateAppSecret(c.Request().Context(), sqlcgen.RotateAppSecretParams{
		ID:               app.ID,
		ClientSecretHash: hash,
	}); err != nil {
		return problem.Internal("rotate: " + err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"client_secret": plain})
}

func (s *Server) deleteDevApp(c echo.Context) error {
	user := userFromContext(c)
	app, err := s.loadOwnedApp(c, user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	q := sqlcgen.New(s.ownerPool)
	if err := q.SoftDeleteApp(c.Request().Context(), app.ID); err != nil {
		return problem.Internal("delete app: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- helpers -----------------------------------------------------------

func (s *Server) loadOwnedApp(c echo.Context, userID int64, slug string) (*sqlcgen.App, error) {
	if s.ownerPool == nil {
		return nil, problem.Internal("dev apps require the owner pool")
	}
	q := sqlcgen.New(s.ownerPool)
	row, err := q.GetAppBySlug(c.Request().Context(), slug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, problem.NotFound("app not found")
		}
		return nil, problem.Internal("load app: " + err.Error())
	}
	if row.OwnerUserID != userID {
		// 404 rather than 403 so we don't leak slug existence to
		// someone scanning for someone else's apps.
		return nil, problem.NotFound("app not found")
	}
	return &row, nil
}

func appToDTO(row *sqlcgen.App) AppDTO {
	manifest, _ := apps.DecodeManifest(row.Manifest)
	return AppDTO{
		ID:          row.ID,
		Slug:        row.Slug,
		Name:        row.Name,
		Description: row.Description,
		ClientID:    row.ClientID,
		Manifest:    manifest,
		OwnerUserID: row.OwnerUserID,
		IsPublic:    row.IsPublic,
		CreatedAt:   row.CreatedAt.Time,
	}
}

func validSlug(s string) bool {
	if len(s) < 3 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' {
			return false
		}
	}
	return true
}
