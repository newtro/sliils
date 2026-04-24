// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// HTTP
	ListenAddr   string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// Logging
	LogLevel  string
	LogFormat string

	// Database
	DatabaseURL string
	AutoMigrate bool

	// Auth
	JWTSigningKey       string
	AccessTokenTTL      time.Duration
	RefreshTokenTTL     time.Duration
	EmailVerifyTTL      time.Duration
	PasswordResetTTL    time.Duration
	MagicLinkTTL        time.Duration
	CookieSecure        bool
	CookieDomain        string
	CookieSameSite      string // "strict" | "lax"
	PublicBaseURL       string // used to build links in emails
	MaxFailedLogins     int
	LoginLockoutMinutes int

	// Email
	ResendAPIKey   string
	EmailFromName  string
	EmailFromEmail string

	// Storage
	StorageBackend string // "local" today; "s3" once SeaweedFS wiring lands
	StorageRoot    string // LocalStorage directory

	// Search (M6 — Meilisearch)
	SearchEnabled     bool          // if false, search endpoints 503 and the indexer is not started
	MeiliURL          string        // e.g. http://localhost:7700
	MeiliMasterKey    string        // Meilisearch master key (used only server-side)
	SearchIndexPrefix string        // prefix so multiple installs can share a Meili instance
	SearchDrainPeriod time.Duration // how often the River worker drains the outbox
	SearchTokenTTL    time.Duration // TTL for per-session tenant tokens issued to clients

	// Calls (M8 — LiveKit)
	CallsEnabled     bool          // if false, call endpoints 503 and the signalling is off
	LiveKitURL       string        // e.g. http://localhost:7880 — used server-side for RoomService RPCs
	LiveKitWSURL     string        // e.g. ws://localhost:7880 — the URL handed to browser clients
	LiveKitAPIKey    string        // LiveKit API key (paired with the secret in the LiveKit config)
	LiveKitAPISecret string        // HMAC secret used to sign join tokens (server-side only)
	CallJoinTokenTTL time.Duration // TTL on the JWT a client uses to connect to a LiveKit room

	// External calendars (M9 — Google/Microsoft OAuth)
	ExternalCalendarsEnabled bool          // when false, /me/external-calendars endpoints 503
	CalendarEncryptionKey    string        // 32 raw bytes or 64 hex chars; AEAD key for refresh tokens
	GoogleOAuthClientID      string        // console.cloud.google.com OAuth client id
	GoogleOAuthClientSecret  string        // paired secret
	GoogleOAuthRedirectURL   string        // must exactly match the console's authorized redirect
	MicrosoftOAuthClientID   string        // aad.portal.azure.com application (client) id
	MicrosoftOAuthClientSecret string      // paired secret
	MicrosoftOAuthRedirectURL  string
	CalendarSyncPeriod       time.Duration // pull-worker cadence (default 60s)

	// Pages (M10 — Yjs + Y-Sweet + Collabora/WOPI)
	PagesEnabled         bool          // master switch for Pages + WOPI endpoints
	YSweetURL            string        // Y-Sweet server URL (e.g. http://localhost:8787)
	YSweetServerToken    string        // optional bearer shared between app + Y-Sweet
	YSweetPublicURL      string        // what browsers see (may differ from server-side URL behind a reverse proxy)
	PageSnapshotPeriod   time.Duration // cadence of the snapshot worker (default 5m)
	PageSnapshotRetention int          // keep the N most recent snapshots per page (default 50)

	// WOPI (Collabora Online)
	CollaboraURL         string        // Collabora Online public URL (empty = UI hides "Open in editor")
	WOPIAccessTokenTTL   time.Duration // TTL on access tokens Collabora hands back on file fetch (default 10m)

	// Push notifications (M11 — VAPID web push + optional external push-proxy)
	PushEnabled      bool   // master switch for /me/devices + push fanout
	VAPIDPublicKey   string // generated once per install; safe to embed in the client bundle
	VAPIDPrivateKey  string // keep secret; signs every web push request
	VAPIDSubject     string // mailto: contact published to push services (e.g. "mailto:ops@sliils.com")
	PushProxyURL     string // empty = APNs/FCM/UnifiedPush stay stubbed
	PushProxyJWT     string // bearer we send to the push-proxy
	PushTenantID     string // our identity to the push-proxy (billing + audit)
	PushTTLSeconds   int    // how long push services should queue undelivered notifications

	// Install secret encryption (stores VAPID private key, Resend API key,
	// LiveKit secret, Y-Sweet token at rest). 32 raw bytes or 64 hex.
	// Required in production; setting a secret without this key is refused.
	SettingsEncryptionKey string

	// Operations / hardening
	//
	// TrustedProxies is a comma-separated list of CIDRs belonging to the
	// reverse proxy in front of the app (e.g. "10.0.0.0/8,192.168.1.0/24").
	// When set, only X-Forwarded-For / X-Real-IP headers arriving from
	// those CIDRs are honoured; everything else uses the direct socket
	// address. Empty = direct socket address only (safe default; rate
	// limits and audit IPs stop trusting attacker-supplied headers).
	TrustedProxies string
	// AllowDevOrigins, when true, adds http://localhost:5173 and
	// http://localhost:1420 to the CORS allow-list. Default false; the
	// dev bootstrap script flips it on for local workstations only.
	AllowDevOrigins bool
}

func Load() (*Config, error) {
	cfg := &Config{
		ListenAddr:   getenv("SLIILS_LISTEN_ADDR", ":8080"),
		LogLevel:     getenv("SLIILS_LOG_LEVEL", "info"),
		LogFormat:    getenv("SLIILS_LOG_FORMAT", "json"),
		ReadTimeout:  getDurationEnv("SLIILS_READ_TIMEOUT", 15*time.Second),
		WriteTimeout: getDurationEnv("SLIILS_WRITE_TIMEOUT", 30*time.Second),

		DatabaseURL: getenv("SLIILS_DATABASE_URL", ""),
		AutoMigrate: getBoolEnv("SLIILS_AUTO_MIGRATE", true),

		JWTSigningKey:       getenv("SLIILS_JWT_SIGNING_KEY", ""),
		AccessTokenTTL:      getDurationEnv("SLIILS_ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL:     getDurationEnv("SLIILS_REFRESH_TOKEN_TTL", 30*24*time.Hour),
		EmailVerifyTTL:      getDurationEnv("SLIILS_EMAIL_VERIFY_TTL", 24*time.Hour),
		PasswordResetTTL:    getDurationEnv("SLIILS_PASSWORD_RESET_TTL", 1*time.Hour),
		MagicLinkTTL:        getDurationEnv("SLIILS_MAGIC_LINK_TTL", 15*time.Minute),
		CookieSecure:        getBoolEnv("SLIILS_COOKIE_SECURE", true),
		CookieDomain:        getenv("SLIILS_COOKIE_DOMAIN", ""),
		CookieSameSite:      strings.ToLower(getenv("SLIILS_COOKIE_SAMESITE", "strict")),
		PublicBaseURL:       strings.TrimRight(getenv("SLIILS_PUBLIC_BASE_URL", "http://localhost:5173"), "/"),
		MaxFailedLogins:     getIntEnv("SLIILS_MAX_FAILED_LOGINS", 10),
		LoginLockoutMinutes: getIntEnv("SLIILS_LOGIN_LOCKOUT_MINUTES", 15),

		ResendAPIKey:   getenv("SLIILS_RESEND_API_KEY", ""),
		EmailFromName:  getenv("SLIILS_EMAIL_FROM_NAME", "SliilS"),
		EmailFromEmail: getenv("SLIILS_EMAIL_FROM", "onboarding@resend.dev"),

		StorageBackend: getenv("SLIILS_STORAGE_BACKEND", "local"),
		StorageRoot:    getenv("SLIILS_STORAGE_ROOT", "./data/files"),

		SearchEnabled:     getBoolEnv("SLIILS_SEARCH_ENABLED", true),
		MeiliURL:          strings.TrimRight(getenv("SLIILS_MEILI_URL", "http://localhost:7700"), "/"),
		MeiliMasterKey:    getenv("SLIILS_MEILI_MASTER_KEY", ""),
		SearchIndexPrefix: getenv("SLIILS_SEARCH_INDEX_PREFIX", "sliils"),
		SearchDrainPeriod: getDurationEnv("SLIILS_SEARCH_DRAIN_PERIOD", 2*time.Second),
		SearchTokenTTL:    getDurationEnv("SLIILS_SEARCH_TOKEN_TTL", 15*time.Minute),

		CallsEnabled:     getBoolEnv("SLIILS_CALLS_ENABLED", true),
		LiveKitURL:       strings.TrimRight(getenv("SLIILS_LIVEKIT_URL", "http://localhost:7880"), "/"),
		LiveKitWSURL:     strings.TrimRight(getenv("SLIILS_LIVEKIT_WS_URL", "ws://localhost:7880"), "/"),
		LiveKitAPIKey:    getenv("SLIILS_LIVEKIT_API_KEY", ""),
		LiveKitAPISecret: getenv("SLIILS_LIVEKIT_API_SECRET", ""),
		CallJoinTokenTTL: getDurationEnv("SLIILS_CALL_JOIN_TOKEN_TTL", 2*time.Hour),

		ExternalCalendarsEnabled:   getBoolEnv("SLIILS_EXTERNAL_CALENDARS_ENABLED", false),
		CalendarEncryptionKey:      getenv("SLIILS_CALENDAR_ENCRYPTION_KEY", ""),
		GoogleOAuthClientID:        getenv("SLIILS_GOOGLE_OAUTH_CLIENT_ID", ""),
		GoogleOAuthClientSecret:    getenv("SLIILS_GOOGLE_OAUTH_CLIENT_SECRET", ""),
		GoogleOAuthRedirectURL:     getenv("SLIILS_GOOGLE_OAUTH_REDIRECT_URL", ""),
		MicrosoftOAuthClientID:     getenv("SLIILS_MICROSOFT_OAUTH_CLIENT_ID", ""),
		MicrosoftOAuthClientSecret: getenv("SLIILS_MICROSOFT_OAUTH_CLIENT_SECRET", ""),
		MicrosoftOAuthRedirectURL:  getenv("SLIILS_MICROSOFT_OAUTH_REDIRECT_URL", ""),
		CalendarSyncPeriod:         getDurationEnv("SLIILS_CALENDAR_SYNC_PERIOD", 60*time.Second),

		PagesEnabled:          getBoolEnv("SLIILS_PAGES_ENABLED", true),
		YSweetURL:             strings.TrimRight(getenv("SLIILS_YSWEET_URL", "http://localhost:8787"), "/"),
		YSweetServerToken:     getenv("SLIILS_YSWEET_SERVER_TOKEN", ""),
		YSweetPublicURL:       strings.TrimRight(getenv("SLIILS_YSWEET_PUBLIC_URL", ""), "/"),
		PageSnapshotPeriod:    getDurationEnv("SLIILS_PAGE_SNAPSHOT_PERIOD", 5*time.Minute),
		PageSnapshotRetention: getIntEnv("SLIILS_PAGE_SNAPSHOT_RETENTION", 50),

		CollaboraURL:       strings.TrimRight(getenv("SLIILS_COLLABORA_URL", ""), "/"),
		WOPIAccessTokenTTL: getDurationEnv("SLIILS_WOPI_ACCESS_TOKEN_TTL", 10*time.Minute),

		PushEnabled:     getBoolEnv("SLIILS_PUSH_ENABLED", true),
		VAPIDPublicKey:  getenv("SLIILS_VAPID_PUBLIC_KEY", ""),
		VAPIDPrivateKey: getenv("SLIILS_VAPID_PRIVATE_KEY", ""),
		VAPIDSubject:    getenv("SLIILS_VAPID_SUBJECT", "mailto:push@sliils.local"),
		PushProxyURL:    strings.TrimRight(getenv("SLIILS_PUSH_PROXY_URL", ""), "/"),
		PushProxyJWT:    getenv("SLIILS_PUSH_PROXY_JWT", ""),
		PushTenantID:    getenv("SLIILS_PUSH_TENANT_ID", ""),
		PushTTLSeconds:  getIntEnv("SLIILS_PUSH_TTL_SECONDS", 86400),

		SettingsEncryptionKey: getenv("SLIILS_SETTINGS_ENCRYPTION_KEY", ""),
		TrustedProxies:        getenv("SLIILS_TRUSTED_PROXIES", ""),
		AllowDevOrigins:       getBoolEnv("SLIILS_ALLOW_DEV_ORIGINS", false),
	}

	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return nil, fmt.Errorf("SLIILS_LOG_FORMAT must be json or text, got %q", cfg.LogFormat)
	}
	if cfg.CookieSameSite != "lax" && cfg.CookieSameSite != "strict" {
		return nil, fmt.Errorf("SLIILS_COOKIE_SAMESITE must be lax or strict, got %q", cfg.CookieSameSite)
	}
	return cfg, nil
}

// Validate enforces required settings for real startup. Tests / CI that spin up
// a minimal server can skip validation and run with zero-value fields.
func (c *Config) Validate() error {
	if c.DatabaseURL == "" {
		return errors.New("SLIILS_DATABASE_URL is required")
	}
	if c.JWTSigningKey == "" {
		return errors.New("SLIILS_JWT_SIGNING_KEY is required (generate: openssl rand -hex 32)")
	}
	if len(c.JWTSigningKey) < 32 {
		return errors.New("SLIILS_JWT_SIGNING_KEY must be at least 32 bytes")
	}
	// Refuse insecure cookies in production. Operators who want http for a
	// local reverse proxy set both PublicBaseURL=http://... AND
	// SLIILS_ALLOW_DEV_ORIGINS=true, which signals "this is not prod".
	if strings.HasPrefix(c.PublicBaseURL, "https://") && !c.CookieSecure {
		return errors.New(
			"SLIILS_COOKIE_SECURE=true is required when SLIILS_PUBLIC_BASE_URL is https (refresh cookie would ride over http hops otherwise)",
		)
	}
	if !strings.HasPrefix(c.PublicBaseURL, "https://") && !c.AllowDevOrigins {
		return errors.New(
			"SLIILS_PUBLIC_BASE_URL must use https in production; set SLIILS_ALLOW_DEV_ORIGINS=true only for local development",
		)
	}
	if c.ResendAPIKey == "" {
		return errors.New("SLIILS_RESEND_API_KEY is required (email is required for signup/magic-link)")
	}
	if c.SearchEnabled {
		if c.MeiliURL == "" {
			return errors.New("SLIILS_MEILI_URL is required when search is enabled")
		}
		if c.MeiliMasterKey == "" {
			return errors.New("SLIILS_MEILI_MASTER_KEY is required when search is enabled")
		}
	}
	if c.ExternalCalendarsEnabled {
		if c.CalendarEncryptionKey == "" {
			return errors.New("SLIILS_CALENDAR_ENCRYPTION_KEY is required when external calendars are enabled (32 raw bytes or 64 hex chars; generate: openssl rand -hex 32)")
		}
		// At least one provider must be wired or the feature does nothing.
		if c.GoogleOAuthClientID == "" && c.MicrosoftOAuthClientID == "" {
			return errors.New("external calendars enabled but no OAuth provider configured; set SLIILS_GOOGLE_OAUTH_CLIENT_ID or SLIILS_MICROSOFT_OAUTH_CLIENT_ID")
		}
	}
	if c.PagesEnabled {
		if c.YSweetURL == "" {
			return errors.New("SLIILS_YSWEET_URL is required when pages are enabled")
		}
		if c.PageSnapshotRetention < 1 {
			return errors.New("SLIILS_PAGE_SNAPSHOT_RETENTION must be >= 1")
		}
	}
	if c.CallsEnabled {
		if c.LiveKitURL == "" {
			return errors.New("SLIILS_LIVEKIT_URL is required when calls are enabled")
		}
		if c.LiveKitWSURL == "" {
			return errors.New("SLIILS_LIVEKIT_WS_URL is required when calls are enabled")
		}
		if c.LiveKitAPIKey == "" {
			return errors.New("SLIILS_LIVEKIT_API_KEY is required when calls are enabled")
		}
		if c.LiveKitAPISecret == "" {
			return errors.New("SLIILS_LIVEKIT_API_SECRET is required when calls are enabled")
		}
	}
	return nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func getBoolEnv(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getIntEnv(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
