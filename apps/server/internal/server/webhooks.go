package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/apps"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/realtime"
)

// Webhooks (M12-P2).
//
// Surface:
//
//   Incoming webhooks (admin creates URL; third parties POST to it):
//     POST   /api/v1/workspaces/:slug/webhooks/incoming       — create
//     GET    /api/v1/workspaces/:slug/webhooks/incoming       — list
//     DELETE /api/v1/webhooks/incoming/:id                    — revoke
//
//   Public receiver (no auth; token in URL path):
//     POST   /api/v1/hooks/:token
//
//   Outgoing webhooks (app installation subscribes to events):
//     POST   /api/v1/installations/:id/webhooks/outgoing      — create
//     GET    /api/v1/installations/:id/webhooks/outgoing      — list
//     DELETE /api/v1/webhooks/outgoing/:id                    — revoke
//
// Outgoing delivery runs through a River worker. Each fired event
// fan-outs to every matching subscription, HMAC-signs the body, and
// POSTs with retries on 5xx.

// ---- DTOs --------------------------------------------------------------

type IncomingWebhookDTO struct {
	ID          int64     `json:"id"`
	WorkspaceID int64     `json:"workspace_id"`
	ChannelID   int64     `json:"channel_id"`
	Name        string    `json:"name"`
	// URL is built server-side from the public base URL + token.
	// The token itself is NOT returned after creation — rotate means
	// delete + create again.
	URL         string    `json:"url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
}

type createIncomingRequest struct {
	ChannelID     int64  `json:"channel_id"`
	Name          string `json:"name"`
	RequireSecret bool   `json:"require_secret"`
}

type createIncomingResponse struct {
	Webhook       IncomingWebhookDTO `json:"webhook"`
	SigningSecret string             `json:"signing_secret,omitempty"` // returned ONCE if require_secret=true
}

type OutgoingWebhookDTO struct {
	ID                  int64     `json:"id"`
	WorkspaceID         int64     `json:"workspace_id"`
	AppInstallationID   int64     `json:"app_installation_id"`
	EventPattern        string    `json:"event_pattern"`
	TargetURL           string    `json:"target_url"`
	CreatedAt           time.Time `json:"created_at"`
}

type createOutgoingRequest struct {
	EventPattern string `json:"event_pattern"`
	TargetURL    string `json:"target_url"`
}

type createOutgoingResponse struct {
	Webhook       OutgoingWebhookDTO `json:"webhook"`
	SigningSecret string             `json:"signing_secret"` // always returned; HMAC-SHA256 key
}

// ---- routes ------------------------------------------------------------

func (s *Server) mountWebhooks(api *echo.Group) {
	authed := api.Group("")
	authed.Use(s.requireAuth())
	authed.POST("/workspaces/:slug/webhooks/incoming", s.createIncomingWebhook)
	authed.GET("/workspaces/:slug/webhooks/incoming", s.listIncomingWebhooks)
	authed.DELETE("/webhooks/incoming/:id", s.deleteIncomingWebhook)

	authed.POST("/installations/:id/webhooks/outgoing", s.createOutgoingWebhook)
	authed.GET("/installations/:id/webhooks/outgoing", s.listOutgoingWebhooks)
	authed.DELETE("/webhooks/outgoing/:id", s.deleteOutgoingWebhook)

	// Public unauthenticated receiver. Token is the auth.
	api.POST("/hooks/:token", s.receiveIncomingWebhook)
}

// ---- incoming: admin CRUD ---------------------------------------------

func (s *Server) createIncomingWebhook(c echo.Context) error {
	user := userFromContext(c)
	slug := c.Param("slug")
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, ws.ID); err != nil {
		return err
	}
	var req createIncomingRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.ChannelID == 0 {
		return problem.BadRequest("name and channel_id are required")
	}

	tokenRaw := make([]byte, 24)
	if _, err := rand.Read(tokenRaw); err != nil {
		return problem.Internal("mint token: " + err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(tokenRaw)

	var plainSecret, secretHash string
	if req.RequireSecret {
		plainSecret, secretHash, err = apps.NewWebhookSecret()
		if err != nil {
			return problem.Internal("mint secret: " + err.Error())
		}
	}

	var row sqlcgen.WebhooksIncoming
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID},
		func(scope db.TxScope) error {
			r, err := scope.Queries.CreateIncomingWebhook(c.Request().Context(), sqlcgen.CreateIncomingWebhookParams{
				WorkspaceID:        ws.ID,
				ChannelID:          req.ChannelID,
				Name:               req.Name,
				Token:              token,
				SigningSecretHash:  secretHash,
				CreatedBy:          &user.ID,
			})
			if err != nil {
				return err
			}
			row = r
			return nil
		})
	if err != nil {
		return problem.Internal("create webhook: " + err.Error())
	}
	return c.JSON(http.StatusCreated, createIncomingResponse{
		Webhook:       incomingToDTO(&row, s.incomingURL(token)),
		SigningSecret: plainSecret,
	})
}

func (s *Server) listIncomingWebhooks(c echo.Context) error {
	user := userFromContext(c)
	slug := c.Param("slug")
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, slug)
	if err != nil {
		return err
	}
	var rows []sqlcgen.WebhooksIncoming
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListIncomingWebhooks(c.Request().Context(), ws.ID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list webhooks: " + err.Error())
	}
	out := make([]IncomingWebhookDTO, 0, len(rows))
	for _, r := range rows {
		// URL is omitted from list responses — post-creation, the
		// token is only visible to whoever captured the creation
		// response. Listing just shows metadata.
		out = append(out, incomingToDTO(&r, ""))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) deleteIncomingWebhook(c echo.Context) error {
	user := userFromContext(c)
	id, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	// Need workspace id to scope the delete; load under owner pool.
	if s.ownerPool == nil {
		return problem.Internal("webhooks require the owner pool")
	}
	var wsID int64
	row := s.ownerPool.QueryRow(c.Request().Context(),
		`SELECT workspace_id FROM webhooks_incoming WHERE id = $1 AND deleted_at IS NULL`, id)
	if err := row.Scan(&wsID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("webhook not found")
		}
		return problem.Internal("load webhook: " + err.Error())
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, wsID); err != nil {
		return err
	}
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: wsID},
		func(scope db.TxScope) error {
			return scope.Queries.DeleteIncomingWebhook(c.Request().Context(), id)
		})
	if err != nil {
		return problem.Internal("delete webhook: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- incoming: public receiver ----------------------------------------

// Incoming payload shape. Minimal v1: text + optional blocks, optional
// thread_ts for threaded replies. Channel comes from the webhook config
// (not from the payload) so each URL posts into exactly one channel.
type incomingPayload struct {
	Text   string        `json:"text,omitempty"`
	Blocks []apps.Block  `json:"blocks,omitempty"`
}

func (s *Server) receiveIncomingWebhook(c echo.Context) error {
	token := c.Param("token")
	if token == "" {
		return problem.BadRequest("missing token")
	}
	if s.ownerPool == nil {
		return problem.Internal("webhooks require the owner pool")
	}
	ownerQ := sqlcgen.New(s.ownerPool)
	wh, err := ownerQ.GetIncomingWebhookByToken(c.Request().Context(), token)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("unknown webhook")
		}
		return problem.Internal("load webhook: " + err.Error())
	}

	body, err := io.ReadAll(http.MaxBytesReader(c.Response().Writer, c.Request().Body, 64*1024))
	if err != nil {
		return problem.BadRequest("read body: " + err.Error())
	}

	// Verify HMAC signature if the webhook was configured to require one.
	if wh.SigningSecretHash != "" {
		tsHeader := c.Request().Header.Get("X-SliilS-Request-Timestamp")
		sigHeader := c.Request().Header.Get("X-SliilS-Signature")
		// The webhook only stores the HASH of the secret — we can't
		// re-derive the signature from a hash. So callers must carry
		// the plaintext secret and sign with it; verification here is
		// "does `plaintext-from-provided-signer-header` hash to the
		// stored hash?" PLUS does the HMAC match?
		//
		// Simpler + what we actually want: we require the caller to
		// present a second header `X-SliilS-Request-Secret` equal to
		// the plaintext secret (out-of-band like a bearer). We verify
		// against the hash with constant time. The HMAC Sign/Verify
		// helpers are still available for callers that want pure
		// webhook-style delivery — used on outgoing side.
		providedSecret := c.Request().Header.Get("X-SliilS-Request-Secret")
		if providedSecret == "" {
			return problem.Unauthorized("missing secret")
		}
		if !apps.VerifyWebhookSecret(providedSecret, wh.SigningSecretHash) {
			return problem.Unauthorized("invalid secret")
		}
		// Additionally verify HMAC if headers are present (optional).
		if tsHeader != "" && sigHeader != "" {
			if err := apps.VerifySignature(providedSecret, tsHeader, sigHeader, body); err != nil {
				return problem.Unauthorized("signature: " + err.Error())
			}
		}
	}

	var payload incomingPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return problem.BadRequest("invalid json: " + err.Error())
	}
	blocksJSON := apps.EncodeBlocks(payload.Blocks)
	if _, err := apps.ValidateBlocksJSON(blocksJSON); err != nil {
		return problem.BadRequest(err.Error())
	}

	// Post the message. Author is the workspace-scoped bot user from
	// the webhook-creator's installation? No — incoming webhooks are
	// NOT tied to an installation (they exist independently). Attribute
	// to the created_by user for audit; the UI shows "$name via Webhook".
	// A future iteration could provision a per-webhook synthetic user.
	created, err := ownerQ.CreateMessage(c.Request().Context(), sqlcgen.CreateMessageParams{
		WorkspaceID:  wh.WorkspaceID,
		ChannelID:    wh.ChannelID,
		AuthorUserID: wh.CreatedBy,
		BodyMd:       payload.Text,
		BodyBlocks:   blocksJSON,
	})
	if err != nil {
		return problem.Internal("create message: " + err.Error())
	}
	_ = ownerQ.TouchIncomingWebhook(c.Request().Context(), wh.ID)

	// Fan out on realtime so subscribers get a live update.
	payload2 := map[string]any{
		"id":         created.ID,
		"channel_id": created.ChannelID,
		"workspace_id": created.WorkspaceID,
		"body_md":    created.BodyMd,
		"created_at": created.CreatedAt.Time,
	}
	b, _ := json.Marshal(payload2)
	s.broker.Publish(realtime.TopicChannel(created.WorkspaceID, created.ChannelID), "message.created", b)

	return c.JSON(http.StatusOK, map[string]any{"ok": true, "message_id": created.ID})
}

// ---- outgoing: CRUD ----------------------------------------------------

func (s *Server) createOutgoingWebhook(c echo.Context) error {
	user := userFromContext(c)
	installID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	install, err := s.loadInstallationForAdmin(c, user.ID, installID)
	if err != nil {
		return err
	}
	var req createOutgoingRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.TargetURL = strings.TrimSpace(req.TargetURL)
	req.EventPattern = strings.TrimSpace(req.EventPattern)
	if req.TargetURL == "" || req.EventPattern == "" {
		return problem.BadRequest("event_pattern and target_url are required")
	}
	if !strings.HasPrefix(req.TargetURL, "http://") && !strings.HasPrefix(req.TargetURL, "https://") {
		return problem.BadRequest("target_url must be http(s)")
	}
	plain, hash, err := apps.NewWebhookSecret()
	if err != nil {
		return problem.Internal("mint secret: " + err.Error())
	}

	var row sqlcgen.WebhooksOutgoing
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: install.WorkspaceID},
		func(scope db.TxScope) error {
			r, err := scope.Queries.CreateOutgoingWebhook(c.Request().Context(), sqlcgen.CreateOutgoingWebhookParams{
				WorkspaceID:         install.WorkspaceID,
				AppInstallationID:   install.ID,
				EventPattern:        req.EventPattern,
				TargetUrl:           req.TargetURL,
				SigningSecretHash:   hash,
			})
			if err != nil {
				return err
			}
			row = r
			return nil
		})
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return problem.Conflict("subscription already exists")
		}
		return problem.Internal("create outgoing webhook: " + err.Error())
	}
	return c.JSON(http.StatusCreated, createOutgoingResponse{
		Webhook:       outgoingToDTO(&row),
		SigningSecret: plain,
	})
}

func (s *Server) listOutgoingWebhooks(c echo.Context) error {
	user := userFromContext(c)
	installID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	install, err := s.loadInstallationForAdmin(c, user.ID, installID)
	if err != nil {
		return err
	}
	var rows []sqlcgen.WebhooksOutgoing
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: install.WorkspaceID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListOutgoingWebhooksForInstallation(c.Request().Context(), install.ID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list outgoing: " + err.Error())
	}
	out := make([]OutgoingWebhookDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, outgoingToDTO(&r))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) deleteOutgoingWebhook(c echo.Context) error {
	user := userFromContext(c)
	id, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	if s.ownerPool == nil {
		return problem.Internal("webhooks require the owner pool")
	}
	var wsID int64
	row := s.ownerPool.QueryRow(c.Request().Context(),
		`SELECT workspace_id FROM webhooks_outgoing WHERE id = $1 AND deleted_at IS NULL`, id)
	if err := row.Scan(&wsID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("webhook not found")
		}
		return problem.Internal("load: " + err.Error())
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, wsID); err != nil {
		return err
	}
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: wsID},
		func(scope db.TxScope) error {
			return scope.Queries.DeleteOutgoingWebhook(c.Request().Context(), id)
		})
	if err != nil {
		return problem.Internal("delete outgoing: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- helpers -----------------------------------------------------------

func (s *Server) loadInstallationForAdmin(c echo.Context, userID, id int64) (*sqlcgen.AppInstallation, error) {
	if s.ownerPool == nil {
		return nil, problem.Internal("apps require the owner pool")
	}
	ownerQ := sqlcgen.New(s.ownerPool)
	row, err := ownerQ.GetInstallationByID(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, problem.NotFound("installation not found")
		}
		return nil, problem.Internal("load installation: " + err.Error())
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), userID, row.WorkspaceID); err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *Server) incomingURL(token string) string {
	base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
	return base + "/api/v1/hooks/" + token
}

func incomingToDTO(r *sqlcgen.WebhooksIncoming, url string) IncomingWebhookDTO {
	var lastUsed *time.Time
	if r.LastUsedAt.Valid {
		t := r.LastUsedAt.Time
		lastUsed = &t
	}
	return IncomingWebhookDTO{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		ChannelID:   r.ChannelID,
		Name:        r.Name,
		URL:         url,
		CreatedAt:   r.CreatedAt.Time,
		LastUsedAt:  lastUsed,
	}
}

func outgoingToDTO(r *sqlcgen.WebhooksOutgoing) OutgoingWebhookDTO {
	return OutgoingWebhookDTO{
		ID:                r.ID,
		WorkspaceID:       r.WorkspaceID,
		AppInstallationID: r.AppInstallationID,
		EventPattern:      r.EventPattern,
		TargetURL:         r.TargetUrl,
		CreatedAt:         r.CreatedAt.Time,
	}
}

// Silence `bytes` + `strconv` if they are unused in future refactors.
var _ = bytes.NewReader
var _ = strconv.Atoi
var _ = fmt.Sprintf
