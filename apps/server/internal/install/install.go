// Package install owns runtime-configurable install-wide + per-workspace
// settings. At v1 it covers two things:
//
//   - Install-wide policy + email fallback (install_settings table). Used
//     for auth flows that happen before a user has a workspace: magic
//     link, password reset, verify-email. Seeded from environment
//     variables on first boot so upgrading from an env-only config is
//     a no-op. One row per key.
//
//   - Per-workspace email overrides (workspace_email_settings table).
//     When a tenant sends an invite, we use their Resend API key +
//     from address so the recipient sees "no-reply@theirdomain.com"
//     rather than the install operator's address. Falls back to the
//     install default when a workspace hasn't configured its own.
//
// Sensitive values (API keys) are stored as ciphertext via the
// internal/secretbox AEAD, keyed by SLIILS_SETTINGS_ENCRYPTION_KEY.
// The key is the same shape as SLIILS_CALENDAR_ENCRYPTION_KEY — 32 raw
// bytes or 64 hex chars.
package install

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/secretbox"
)

// Keys is the catalogue of install_settings rows the app reads. Kept
// as exported consts so typos bubble up as compile errors.
const (
	// SignupMode — "open" (anyone can /auth/signup and create a workspace)
	// or "invite_only" (signup requires an accepted invite first).
	// Default on a fresh install is "invite_only" — safer for
	// self-host operators who just want their own team.
	KeySignupMode = "signup.mode"

	// Install-wide email provider used for magic-link / password-reset /
	// verify-email flows (things that happen before any workspace exists).
	KeyInstallEmailProvider   = "email.provider"      // "resend" | "smtp"
	KeyInstallResendAPIKey    = "email.resend_api_key"
	KeyInstallEmailFrom       = "email.from_address"
	KeyInstallEmailFromName   = "email.from_name"

	// Whether the install has finished its first-run wizard. Blocks the
	// wizard from re-appearing after the first admin fills it in.
	KeyInstallSetupCompleted = "install.setup_completed"

	// Infrastructure endpoints. All are optional; when empty, the
	// server falls back to the environment variable it was started
	// with. An operator can flip between env and DB without downtime.
	KeyVAPIDPublicKey    = "vapid.public_key"
	KeyVAPIDPrivateKey   = "vapid.private_key"
	KeyVAPIDSubject      = "vapid.subject"
	KeyCollaboraURL      = "collabora.url"
	KeyYSweetURL         = "ysweet.url"
	KeyYSweetServerToken = "ysweet.server_token"
	KeyLiveKitURL        = "livekit.url"
	KeyLiveKitWSURL      = "livekit.ws_url"
	KeyLiveKitAPIKey     = "livekit.api_key"
	KeyLiveKitAPISecret  = "livekit.api_secret"
)

// SignupMode values.
const (
	SignupOpen       = "open"
	SignupInviteOnly = "invite_only"
)

// Service bundles all the reads + writes callers need. Construct it
// once at server bootstrap, pass into handlers via the server.Server
// struct.
type Service struct {
	pool *pgxpool.Pool
	box  *secretbox.Box // nil when no encryption key is configured
}

// NewService — pool must be the owner pool (install_settings has no RLS
// policy; workspace_email_settings does and handlers use the tenant
// pool for those specific calls). The encryption box is optional at
// v1 — when nil, "encrypted" values store as plaintext with a clear
// log warning. This keeps dev boot working without the operator
// having to set yet another key up front.
func NewService(pool *pgxpool.Pool, box *secretbox.Box) *Service {
	return &Service{pool: pool, box: box}
}

// ---- install_settings --------------------------------------------------

// Get returns a single install setting by key. Returns ("", nil) when
// the row doesn't exist yet — callers should fall back to a default.
func (s *Service) Get(ctx context.Context, key string) (string, error) {
	q := sqlcgen.New(s.pool)
	row, err := q.GetInstallSetting(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("install: get %q: %w", key, err)
	}
	if !row.Encrypted {
		return row.Value, nil
	}
	if s.box == nil {
		// Settings flag the row as encrypted but we have no key to
		// decrypt — treat as missing so callers fall back. The admin
		// will see this as "configure the key or re-enter the value."
		return "", nil
	}
	plain, err := s.box.Decrypt(row.Value)
	if err != nil {
		return "", fmt.Errorf("install: decrypt %q: %w", key, err)
	}
	return string(plain), nil
}

// Set upserts a key. When `encrypted` is true and an encryption box is
// configured, the value is sealed before write; otherwise it goes in
// plaintext and the `encrypted` flag still records the caller's intent
// so future reads know how to treat it if a key is added later.
func (s *Service) Set(ctx context.Context, key, value string, encrypted bool, actorID *int64) error {
	stored := value
	if encrypted && s.box != nil && value != "" {
		sealed, err := s.box.Encrypt([]byte(value))
		if err != nil {
			return fmt.Errorf("install: encrypt %q: %w", key, err)
		}
		stored = sealed
	}
	q := sqlcgen.New(s.pool)
	return q.UpsertInstallSetting(ctx, sqlcgen.UpsertInstallSettingParams{
		Key:       key,
		Value:     stored,
		Encrypted: encrypted && s.box != nil,
		UpdatedBy: actorID,
	})
}

// Seed writes a default if the key is NOT already present. Used at
// boot to pull env defaults into the DB so admins can later edit
// through the UI without losing the env-configured starting point.
func (s *Service) Seed(ctx context.Context, key, value string, encrypted bool) error {
	if value == "" {
		return nil
	}
	stored := value
	// Even the seed path respects the encryption box when present —
	// leaking the env value into DB ciphertext is strictly better
	// than leaking it as plaintext.
	if encrypted && s.box != nil {
		sealed, err := s.box.Encrypt([]byte(value))
		if err != nil {
			return fmt.Errorf("install: seed encrypt %q: %w", key, err)
		}
		stored = sealed
	}
	q := sqlcgen.New(s.pool)
	return q.SeedInstallSettingIfAbsent(ctx, sqlcgen.SeedInstallSettingIfAbsentParams{
		Key:       key,
		Value:     stored,
		Encrypted: encrypted && s.box != nil,
	})
}

// SignupMode returns the effective policy. Defaults to invite_only when
// the setting has never been written (safer for fresh installs).
func (s *Service) SignupMode(ctx context.Context) string {
	v, _ := s.Get(ctx, KeySignupMode)
	switch v {
	case SignupOpen, SignupInviteOnly:
		return v
	default:
		return SignupInviteOnly
	}
}

// SetupCompleted reports whether the first-run wizard has been
// finished. When false, the web client routes to a setup screen
// instead of the normal auth flow.
func (s *Service) SetupCompleted(ctx context.Context) bool {
	v, _ := s.Get(ctx, KeyInstallSetupCompleted)
	return v == "true"
}

// ---- workspace_email_settings ------------------------------------------

// WorkspaceEmailConfig is what callers receive; API-key plaintext
// is never returned, only an APIKeyIsSet flag.
type WorkspaceEmailConfig struct {
	Provider    string
	FromAddress string
	FromName    string
	APIKeyIsSet bool
	// ResolvedAPIKey is the plaintext for internal use (email sender).
	// Never marshal this to clients.
	ResolvedAPIKey string
}

// GetWorkspaceEmail returns the effective per-workspace email
// configuration, decrypting the stored API key. When the workspace
// has no row, APIKeyIsSet is false — callers should fall back to the
// install default.
func (s *Service) GetWorkspaceEmail(ctx context.Context, workspaceID int64) (WorkspaceEmailConfig, error) {
	q := sqlcgen.New(s.pool)
	row, err := q.GetWorkspaceEmailSettings(ctx, workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceEmailConfig{}, nil
		}
		return WorkspaceEmailConfig{}, err
	}
	cfg := WorkspaceEmailConfig{
		Provider:    row.Provider,
		FromAddress: row.FromAddress,
		FromName:    row.FromName,
		APIKeyIsSet: row.ResendApiKeyEnc != "",
	}
	if row.ResendApiKeyEnc != "" && s.box != nil {
		plain, err := s.box.Decrypt(row.ResendApiKeyEnc)
		if err != nil {
			return cfg, fmt.Errorf("install: decrypt workspace %d api key: %w", workspaceID, err)
		}
		cfg.ResolvedAPIKey = string(plain)
	}
	return cfg, nil
}

// SetWorkspaceEmail upserts a workspace's email configuration.
// When apiKey is empty the existing encrypted value is preserved (the
// UI can update from_address without re-entering the secret).
func (s *Service) SetWorkspaceEmail(
	ctx context.Context,
	workspaceID int64,
	provider, apiKey, fromAddress, fromName string,
	actorID *int64,
) (WorkspaceEmailConfig, error) {
	enc := ""
	if apiKey != "" {
		if s.box == nil {
			return WorkspaceEmailConfig{}, errors.New(
				"install: refusing to store workspace API key in plaintext — set SLIILS_SETTINGS_ENCRYPTION_KEY",
			)
		}
		sealed, err := s.box.Encrypt([]byte(apiKey))
		if err != nil {
			return WorkspaceEmailConfig{}, fmt.Errorf("install: encrypt workspace api key: %w", err)
		}
		enc = sealed
	}
	q := sqlcgen.New(s.pool)
	row, err := q.UpsertWorkspaceEmailSettings(ctx, sqlcgen.UpsertWorkspaceEmailSettingsParams{
		WorkspaceID:      workspaceID,
		Provider:         provider,
		ResendApiKeyEnc:  enc,
		FromAddress:      fromAddress,
		FromName:         fromName,
		UpdatedBy:        actorID,
	})
	if err != nil {
		return WorkspaceEmailConfig{}, err
	}
	cfg := WorkspaceEmailConfig{
		Provider:    row.Provider,
		FromAddress: row.FromAddress,
		FromName:    row.FromName,
		APIKeyIsSet: row.ResendApiKeyEnc != "",
	}
	if row.ResendApiKeyEnc != "" && s.box != nil {
		plain, _ := s.box.Decrypt(row.ResendApiKeyEnc)
		cfg.ResolvedAPIKey = string(plain)
	}
	return cfg, nil
}

// DeleteWorkspaceEmail removes a workspace's overrides so invite emails
// fall back to the install default.
func (s *Service) DeleteWorkspaceEmail(ctx context.Context, workspaceID int64) error {
	q := sqlcgen.New(s.pool)
	return q.DeleteWorkspaceEmailSettings(ctx, workspaceID)
}
