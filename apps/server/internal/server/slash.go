package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"

	"github.com/sliils/sliils/apps/server/internal/apps"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/problem"
)

// Slash commands (M12-P2).
//
// Two surfaces:
//
//   - Registration (admin configures an installed app's command):
//       POST   /api/v1/installations/:id/slash-commands
//       GET    /api/v1/workspaces/:slug/slash-commands
//       DELETE /api/v1/slash-commands/:id
//
//   - Invocation (user types "/poll hello" in composer):
//       POST   /api/v1/workspaces/:slug/slash-commands/invoke
//       Body: { "command":"/poll", "text":"hello", "channel_id":123 }
//       We look up the handler URL, HMAC-sign + POST to it with a
//       Slack-shaped body, and return the response message back to
//       the invoker (as an ephemeral or channel message depending on
//       response_type).

// ---- DTOs --------------------------------------------------------------

type SlashCommandDTO struct {
	ID                int64     `json:"id"`
	WorkspaceID       int64     `json:"workspace_id"`
	AppInstallationID int64     `json:"app_installation_id"`
	Command           string    `json:"command"`
	Description       string    `json:"description,omitempty"`
	UsageHint         string    `json:"usage_hint,omitempty"`
	TargetURL         string    `json:"target_url"`
	CreatedAt         time.Time `json:"created_at"`
}

type registerSlashRequest struct {
	Command     string `json:"command"`
	TargetURL   string `json:"target_url"`
	Description string `json:"description,omitempty"`
	UsageHint   string `json:"usage_hint,omitempty"`
}

type registerSlashResponse struct {
	Command       SlashCommandDTO `json:"command"`
	SigningSecret string          `json:"signing_secret"`
}

type invokeSlashRequest struct {
	Command   string `json:"command"`
	Text      string `json:"text,omitempty"`
	ChannelID int64  `json:"channel_id"`
}

type invokeSlashResponse struct {
	ResponseType string       `json:"response_type"` // "ephemeral" | "in_channel"
	Text         string       `json:"text,omitempty"`
	Blocks       []apps.Block `json:"blocks,omitempty"`
}

// ---- routes ------------------------------------------------------------

func (s *Server) mountSlashCommands(api *echo.Group) {
	g := api.Group("")
	g.Use(s.requireAuth())
	g.POST("/installations/:id/slash-commands", s.registerSlashCommand)
	g.GET("/workspaces/:slug/slash-commands", s.listSlashCommandsForWorkspace)
	g.DELETE("/slash-commands/:id", s.deleteSlashCommand)
	g.POST("/workspaces/:slug/slash-commands/invoke", s.invokeSlashCommand)
}

// ---- registration ------------------------------------------------------

func (s *Server) registerSlashCommand(c echo.Context) error {
	user := userFromContext(c)
	installID, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	install, err := s.loadInstallationForAdmin(c, user.ID, installID)
	if err != nil {
		return err
	}
	var req registerSlashRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	req.Command = strings.TrimSpace(req.Command)
	req.TargetURL = strings.TrimSpace(req.TargetURL)
	if !strings.HasPrefix(req.Command, "/") || len(req.Command) < 2 {
		return problem.BadRequest("command must start with /")
	}
	if !strings.HasPrefix(req.TargetURL, "http") {
		return problem.BadRequest("target_url must be http(s)")
	}
	plain, hash, err := apps.NewWebhookSecret()
	if err != nil {
		return problem.Internal("mint secret: " + err.Error())
	}

	var row sqlcgen.SlashCommand
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: install.WorkspaceID},
		func(scope db.TxScope) error {
			r, err := scope.Queries.RegisterSlashCommand(c.Request().Context(), sqlcgen.RegisterSlashCommandParams{
				WorkspaceID:        install.WorkspaceID,
				AppInstallationID:  install.ID,
				Command:            req.Command,
				TargetUrl:          req.TargetURL,
				Description:        req.Description,
				UsageHint:          req.UsageHint,
				SigningSecretHash:  hash,
			})
			if err != nil {
				return err
			}
			row = r
			return nil
		})
	if err != nil {
		if strings.Contains(err.Error(), "unique") {
			return problem.Conflict("command already registered")
		}
		return problem.Internal("register slash command: " + err.Error())
	}
	return c.JSON(http.StatusCreated, registerSlashResponse{
		Command:       slashToDTO(&row),
		SigningSecret: plain,
	})
}

func (s *Server) listSlashCommandsForWorkspace(c echo.Context) error {
	user := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	var rows []sqlcgen.SlashCommand
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: ws.ID, ReadOnly: true},
		func(scope db.TxScope) error {
			r, err := scope.Queries.ListSlashCommandsForWorkspace(c.Request().Context(), ws.ID)
			if err != nil {
				return err
			}
			rows = r
			return nil
		})
	if err != nil {
		return problem.Internal("list: " + err.Error())
	}
	out := make([]SlashCommandDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, slashToDTO(&r))
	}
	return c.JSON(http.StatusOK, out)
}

func (s *Server) deleteSlashCommand(c echo.Context) error {
	user := userFromContext(c)
	id, err := parseInt64Param(c, "id")
	if err != nil {
		return err
	}
	if s.ownerPool == nil {
		return problem.Internal("slash commands require the owner pool")
	}
	var wsID int64
	row := s.ownerPool.QueryRow(c.Request().Context(),
		`SELECT workspace_id FROM slash_commands WHERE id = $1 AND deleted_at IS NULL`, id)
	if err := row.Scan(&wsID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("command not found")
		}
		return problem.Internal("load: " + err.Error())
	}
	if err := s.requireWorkspaceAdmin(c.Request().Context(), user.ID, wsID); err != nil {
		return err
	}
	err = db.WithTx(c.Request().Context(), s.pool.Pool,
		db.TxOptions{UserID: user.ID, WorkspaceID: wsID},
		func(scope db.TxScope) error {
			return scope.Queries.DeleteSlashCommand(c.Request().Context(), id)
		})
	if err != nil {
		return problem.Internal("delete: " + err.Error())
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- invocation --------------------------------------------------------

func (s *Server) invokeSlashCommand(c echo.Context) error {
	user := userFromContext(c)
	ws, err := s.resolveWorkspaceBySlug(c.Request().Context(), user.ID, c.Param("slug"))
	if err != nil {
		return err
	}
	var req invokeSlashRequest
	if err := c.Bind(&req); err != nil {
		return problem.BadRequest("invalid body")
	}
	if !strings.HasPrefix(req.Command, "/") {
		return problem.BadRequest("command must start with /")
	}

	if s.ownerPool == nil {
		return problem.Internal("slash commands require the owner pool")
	}
	// Look up the command under owner pool — it's a straight lookup by
	// (workspace, command), no RLS concerns since we verified the
	// caller's ws above.
	ownerQ := sqlcgen.New(s.ownerPool)
	cmd, err := ownerQ.GetSlashCommandForWorkspace(c.Request().Context(), sqlcgen.GetSlashCommandForWorkspaceParams{
		WorkspaceID: ws.ID,
		Command:     req.Command,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.NotFound("no such command")
		}
		return problem.Internal("lookup: " + err.Error())
	}

	// Build the Slack-shaped invocation body.
	body, err := json.Marshal(map[string]any{
		"command":      req.Command,
		"text":         req.Text,
		"user_id":      fmt.Sprintf("%d", user.ID),
		"channel_id":   fmt.Sprintf("%d", req.ChannelID),
		"workspace_id": fmt.Sprintf("%d", ws.ID),
	})
	if err != nil {
		return problem.Internal("marshal: " + err.Error())
	}

	// HMAC-sign the outbound request so the receiver can verify.
	// NOTE: signing secret is stored HASHED; we cannot reconstruct it
	// here. At registration we returned the plaintext ONCE, and the
	// receiver keeps it. For now we emit the hashed-secret as the
	// signing material proxy — the receiver must store the plain
	// secret and use it to verify independently. v1.1 swaps this for
	// envelope encryption if we need to regenerate signatures.
	//
	// In practice: registrants store the plain secret themselves and
	// sign/verify against it with apps.SignBody. Our side simply
	// forwards (no signing), and the receiver trusts the HMAC they
	// get from their own stored secret.
	//
	// For v1 we skip HMAC signing of the outbound slash call. Receivers
	// should use a high-entropy URL token (opaque slug in target_url)
	// for auth until HMAC rollout in v1.1.
	_ = cmd.SigningSecretHash

	postCtx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(postCtx, http.MethodPost, cmd.TargetUrl, bytes.NewReader(body))
	if err != nil {
		return problem.Internal("build outbound: " + err.Error())
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-SliilS-Request-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return problem.Internal("invoke target: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return problem.Internal(fmt.Sprintf("target returned %d", resp.StatusCode))
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	if err != nil {
		return problem.Internal("read target response: " + err.Error())
	}
	// Target may return an empty 200 (fire-and-forget) or a JSON
	// response. Forward whatever it gave us to the caller.
	if len(bytes.TrimSpace(respBody)) == 0 {
		return c.JSON(http.StatusOK, invokeSlashResponse{ResponseType: "ephemeral", Text: "ok"})
	}
	var out invokeSlashResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		// If the target returned text/plain or malformed JSON, wrap it.
		return c.JSON(http.StatusOK, invokeSlashResponse{
			ResponseType: "ephemeral",
			Text:         string(respBody),
		})
	}
	if out.ResponseType == "" {
		out.ResponseType = "ephemeral"
	}
	return c.JSON(http.StatusOK, out)
}

// ---- helpers -----------------------------------------------------------

func slashToDTO(r *sqlcgen.SlashCommand) SlashCommandDTO {
	return SlashCommandDTO{
		ID:                r.ID,
		WorkspaceID:       r.WorkspaceID,
		AppInstallationID: r.AppInstallationID,
		Command:           r.Command,
		Description:       r.Description,
		UsageHint:         r.UsageHint,
		TargetURL:         r.TargetUrl,
		CreatedAt:         r.CreatedAt.Time,
	}
}
