// Package server owns the HTTP router and mounts all handlers.
package server

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/sliils/sliils/apps/server/internal/audit"
	"github.com/sliils/sliils/apps/server/internal/auth"
	"github.com/sliils/sliils/apps/server/internal/calls"
	"github.com/sliils/sliils/apps/server/internal/calsync"
	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/email"
	"github.com/sliils/sliils/apps/server/internal/health"
	"github.com/sliils/sliils/apps/server/internal/install"
	"github.com/sliils/sliils/apps/server/internal/pages"
	"github.com/sliils/sliils/apps/server/internal/problem"
	"github.com/sliils/sliils/apps/server/internal/push"
	"github.com/sliils/sliils/apps/server/internal/ratelimit"
	"github.com/sliils/sliils/apps/server/internal/realtime"
	"github.com/sliils/sliils/apps/server/internal/search"
	"github.com/sliils/sliils/apps/server/internal/storage"
	"github.com/sliils/sliils/apps/server/internal/wopi"
)

//go:embed splash.html
var splashHTML string

// Server is the root HTTP application. It is the assembly point for every
// dependency an HTTP handler might need.
type Server struct {
	cfg      *config.Config
	logger   *slog.Logger
	echo     *echo.Echo
	health   *health.Registry
	pool     *db.Pool
	queries  *sqlcgen.Queries
	tokens   *auth.TokenIssuer
	hasher   *auth.PasswordHasher
	email    email.Sender
	limiter  *ratelimit.Limiter
	auditor  *audit.Recorder
	broker   *realtime.Broker
	presence *realtime.Presence
	typing   *realtime.Typing
	storage   storage.Store
	search    *search.Service
	ownerPool *pgxpool.Pool     // nil when search disabled; used for RLS-bypassing work
	calls     *calls.Client     // nil when calls disabled
	calSync   *calsync.Service  // nil when external calendars disabled
	ySweet    *pages.Client     // nil when pages are disabled
	collabora *wopi.DiscoveryClient // nil when SLIILS_COLLABORA_URL is empty
	wopiTokens *wopi.TokenIssuer // always wired when Pages are enabled (WOPI + non-Collabora uses share the issuer)
	push      *push.Service     // nil when push is disabled
	installSvc *install.Service // install_settings + workspace_email_settings
	enqueueCalPush CalendarPushEnqueueFunc // nil when no worker runner is wired
	enqueuePush    PushEnqueueFunc         // nil when no worker runner is wired
}

// PushEnqueueFunc hands a single-recipient push job to the worker runner.
// Wired in main.go after the runner is built (avoids server→workers
// import cycle). Signature mirrors the fan-out payload shape.
type PushEnqueueFunc func(ctx context.Context, userID int64, msgID, notifType, channelID string) error

// SetPushEnqueue is called from main after the Runner is built, so
// mention/DM handlers can kick off push jobs.
func (s *Server) SetPushEnqueue(f PushEnqueueFunc) { s.enqueuePush = f }

// CalendarPushEnqueueFunc forwards a calendar-push job to the worker
// runner. main.go wires this at startup to avoid a server→workers
// import cycle.
type CalendarPushEnqueueFunc func(ctx context.Context, eventID, userID int64, action string) error

// SetCalendarPushEnqueue is called from main after the Runner is built,
// so handlers can kick off push jobs when an event is created / updated
// / cancelled. Safe to leave nil in tests; the shim no-ops.
func (s *Server) SetCalendarPushEnqueue(f CalendarPushEnqueueFunc) {
	s.enqueueCalPush = f
}

// Options collects optional dependencies. Fields left nil fall back to
// production implementations that require real config (DB, Resend, etc.).
// Tests use this to inject fakes without hitting the network or disk.
type Options struct {
	EmailSender   email.Sender
	Limiter       *ratelimit.Limiter
	Storage       storage.Store
	SearchClient  *search.Client
	SearchTokens  *search.TokenIssuer
	SearchOwnerDB *pgxpool.Pool // unused directly; kept for forward-compat
	CallsClient   *calls.Client // nil = calls disabled (endpoints 503)
	CalSync       *calsync.Service // nil = external calendars disabled
	YSweet        *pages.Client   // nil = pages are disabled (create/auth endpoints 503)
	Push          *push.Service   // nil = push is disabled
	Install       *install.Service // install_settings + per-workspace email config
}

func New(cfg *config.Config, logger *slog.Logger, pool *db.Pool, opts Options) (*Server, error) {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.HTTPErrorHandler = problem.ErrorHandler(logger)

	// Resolve the client IP from a trusted proxy's X-Forwarded-For header
	// only if SLIILS_TRUSTED_PROXIES lists the proxy's CIDR. Otherwise
	// use the direct socket address so attacker-supplied headers can't
	// forge IPs into rate-limit keys or the audit log.
	if trusted := parseTrustedProxies(cfg.TrustedProxies); len(trusted) > 0 {
		opts := make([]echo.TrustOption, 0, len(trusted))
		for _, cidr := range trusted {
			opts = append(opts, echo.TrustIPRange(cidr))
		}
		e.IPExtractor = echo.ExtractIPFromXFFHeader(opts...)
	} else {
		e.IPExtractor = echo.ExtractIPDirect()
	}

	e.Use(middleware.Recover())
	e.Use(middleware.RequestID())
	e.Use(slogRequestLogger(logger))
	// HSTS + CSP: HSTSMaxAge locks browsers to https for a year (no-op on
	// http, harmless to send); CSP pins script/style/connect to same-origin
	// so a stored XSS can't phone home or embed a foreign script tag.
	e.Use(middleware.SecureWithConfig(middleware.SecureConfig{
		XSSProtection:         "1; mode=block",
		ContentTypeNosniff:    "nosniff",
		XFrameOptions:         "SAMEORIGIN",
		HSTSMaxAge:            31536000,
		HSTSExcludeSubdomains: false,
		ContentSecurityPolicy: "default-src 'self'; img-src 'self' data: blob: https:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com; script-src 'self'; connect-src 'self' wss: https:; frame-ancestors 'self'; base-uri 'self'; form-action 'self'",
		ReferrerPolicy:        "strict-origin-when-cross-origin",
	}))
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     allowedOrigins(cfg),
		AllowMethods:     []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowHeaders:     []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAuthorization, echo.HeaderAccept, "Idempotency-Key"},
		AllowCredentials: true,
	}))

	var queries *sqlcgen.Queries
	if pool != nil {
		queries = sqlcgen.New(pool.Pool)
	}

	sender := opts.EmailSender
	if sender == nil && cfg.ResendAPIKey != "" {
		sender = email.NewResendSender(cfg.ResendAPIKey, cfg.EmailFromName, cfg.EmailFromEmail, logger)
	}

	limiter := opts.Limiter
	if limiter == nil {
		limiter = ratelimit.New()
	}

	store := opts.Storage
	if store == nil && cfg.StorageBackend == "local" {
		local, err := storage.NewLocalStorage(cfg.StorageRoot)
		if err != nil {
			return nil, fmt.Errorf("init local storage: %w", err)
		}
		store = local
	}

	broker := realtime.NewBroker()
	s := &Server{
		cfg:      cfg,
		logger:   logger,
		echo:     e,
		health:   health.NewRegistry(),
		pool:     pool,
		queries:  queries,
		tokens:   auth.NewTokenIssuer([]byte(cfg.JWTSigningKey), cfg.AccessTokenTTL),
		hasher:   auth.NewDefaultHasher(),
		email:    sender,
		limiter:  limiter,
		auditor:  audit.NewRecorder(queries, logger),
		broker:   broker,
		presence: realtime.NewPresence(broker),
		typing:   realtime.NewTyping(broker),
		storage:  store,
	}

	if pool != nil {
		s.health.Register("postgres", func(ctx context.Context) error {
			return pool.Ping(ctx)
		})
	}

	if opts.SearchClient != nil && pool != nil {
		s.search = search.NewService(opts.SearchClient, opts.SearchTokens, pool.Pool, logger)
		s.health.Register("meilisearch", func(ctx context.Context) error {
			return opts.SearchClient.Health(ctx)
		})
	}
	// Owner pool is wired whenever the caller provides one. The invite
	// accept path needs it even when search is off because token lookup
	// happens before the user has a workspace-scoped session.
	s.ownerPool = opts.SearchOwnerDB

	if opts.CallsClient != nil {
		s.calls = opts.CallsClient
		s.health.Register("livekit", func(ctx context.Context) error {
			return opts.CallsClient.Health(ctx)
		})
	}
	if opts.CalSync != nil {
		s.calSync = opts.CalSync
	}
	if opts.YSweet != nil {
		s.ySweet = opts.YSweet
		s.health.Register("y-sweet", func(ctx context.Context) error {
			return opts.YSweet.Health(ctx)
		})
	}
	// WOPI token issuance reuses the JWT signing key so we don't have
	// to manage a separate secret; the audience claim keeps WOPI tokens
	// from being mistaken for normal access tokens.
	if cfg.PagesEnabled {
		s.wopiTokens = wopi.NewTokenIssuer([]byte(cfg.JWTSigningKey), cfg.WOPIAccessTokenTTL)
	}
	if cfg.CollaboraURL != "" {
		s.collabora = wopi.NewDiscoveryClient(cfg.CollaboraURL, logger)
	}
	if opts.Push != nil {
		s.push = opts.Push
	}
	if opts.Install != nil {
		s.installSvc = opts.Install
	}

	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.echo.GET("/", s.splashPage)
	s.echo.GET("/healthz", health.Handler())
	s.echo.GET("/readyz", s.health.ReadyHandler())

	api := s.echo.Group("/api/v1")
	api.GET("/ping", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"pong": "sliils"})
	})

	s.mountAuth(api)
	s.mountMe(api)
	s.mountWorkspaces(api)
	s.mountMessages(api)
	s.mountFiles(api)
	s.mountWS(api)
	s.mountSearch(api)
	s.mountInvites(api)
	s.mountDMs(api)
	s.mountMeetings(api)
	s.mountEvents(api)
	s.mountICal(api)
	s.mountExternalCalendars(api)
	s.mountPages(api)
	s.mountWOPI(api)
	s.mountDevApps(api)
	s.mountOAuthApps(api)
	s.mountWebhooks(api)
	s.mountBotAPI(api)
	s.mountSlashCommands(api)
	s.mountAdmin(api)
	s.mountAdminIntegrations(api)
	s.mountChannels(api)
	s.mountFirstRun(api)
}

func (s *Server) splashPage(c echo.Context) error {
	return c.HTML(http.StatusOK, splashHTML)
}

func (s *Server) Start() error {
	s.echo.Server.ReadTimeout = s.cfg.ReadTimeout
	s.echo.Server.WriteTimeout = s.cfg.WriteTimeout
	s.logger.Info("HTTP server listening", slog.String("addr", s.cfg.ListenAddr))
	return s.echo.Start(s.cfg.ListenAddr)
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.echo.Shutdown(ctx)
}

// Handler exposes the underlying http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.echo
}

// Health returns the readiness registry so callers can register dependencies.
func (s *Server) Health() *health.Registry {
	return s.health
}

// Broker exposes the in-process realtime broker so main.go can wire
// background workers (calendar reminders, etc.) into the same fanout
// channels as the HTTP handlers.
func (s *Server) Broker() *realtime.Broker {
	return s.broker
}

// resolveEmailSenderForWorkspace picks the right outbound email sender
// for a workspace-originated email (invites today; workspace
// notifications later). Resolution order:
//
//  1. workspace_email_settings row — tenant-configured Resend API key
//     + from address. This is the normal case for multi-tenant installs
//     where each workspace sends as their own domain.
//  2. install_settings (email.resend_api_key + email.from_address) —
//     operator-level fallback for tenants that haven't configured their
//     own yet.
//  3. The env-time s.email sender (set up in server.New from .env).
//
// Returns (nil, nil) when no provider is available — the caller
// should report "email not configured" rather than silently dropping.
func (s *Server) resolveEmailSenderForWorkspace(ctx context.Context, workspaceID int64) (email.Sender, error) {
	if s.installSvc != nil {
		cfg, err := s.installSvc.GetWorkspaceEmail(ctx, workspaceID)
		if err == nil && cfg.APIKeyIsSet && cfg.ResolvedAPIKey != "" && cfg.FromAddress != "" {
			return email.NewResendSender(
				cfg.ResolvedAPIKey,
				firstNonEmpty(cfg.FromName, s.cfg.EmailFromName),
				cfg.FromAddress,
				s.logger,
			), nil
		}
	}
	return s.resolveInstallEmailSender(ctx), nil
}

// resolveInstallEmailSender returns the sender used for install-wide flows
// (magic-link, password-reset, verify-email). Resolution order:
//
//  1. install_settings (email.resend_api_key + email.from_address) —
//     the DB-managed value wins once an admin has set one via the
//     wizard or Admin → Integrations. This is the key fix that makes
//     post-boot email configuration actually take effect: without it,
//     s.email was frozen at server startup and stayed nil forever if
//     the operator skipped email at first-run.
//  2. The env-time s.email sender (set up in server.New from .env).
//
// Returns nil when nothing is configured so callers can surface
// "email not configured" rather than crashing.
func (s *Server) resolveInstallEmailSender(ctx context.Context) email.Sender {
	if s.installSvc != nil {
		apiKey, _ := s.installSvc.Get(ctx, install.KeyInstallResendAPIKey)
		fromAddr, _ := s.installSvc.Get(ctx, install.KeyInstallEmailFrom)
		fromName, _ := s.installSvc.Get(ctx, install.KeyInstallEmailFromName)
		if apiKey != "" && fromAddr != "" {
			return email.NewResendSender(
				apiKey,
				firstNonEmpty(fromName, s.cfg.EmailFromName),
				fromAddr,
				s.logger,
			)
		}
	}
	return s.email
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// allowedOrigins returns the CORS allow-list. PublicBaseURL is always
// included; dev-only origins are added when SLIILS_ALLOW_DEV_ORIGINS=true
// so local vite / Tauri can talk to a dev server without poking a hole
// into a production install.
func allowedOrigins(cfg *config.Config) []string {
	out := []string{cfg.PublicBaseURL}
	if cfg.AllowDevOrigins {
		out = append(out, "http://localhost:5173", "http://localhost:1420")
	}
	return out
}

// parseTrustedProxies parses a comma-separated CIDR list. Any malformed
// entry is silently dropped — operators who mis-type a CIDR get the
// "no trusted proxies" behaviour (safe default) rather than a crash.
func parseTrustedProxies(raw string) []*net.IPNet {
	if raw == "" {
		return nil
	}
	var out []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if _, ipnet, err := net.ParseCIDR(p); err == nil {
			out = append(out, ipnet)
		}
	}
	return out
}

func slogRequestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:      true,
		LogStatus:   true,
		LogMethod:   true,
		LogLatency:  true,
		LogRemoteIP: true,
		LogError:    true,
		HandleError: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("uri", v.URI),
				slog.Int("status", v.Status),
				slog.Duration("latency", v.Latency),
				slog.String("remote_ip", v.RemoteIP),
			}
			if v.Error != nil {
				attrs = append(attrs, slog.String("error", v.Error.Error()))
				logger.LogAttrs(c.Request().Context(), slog.LevelError, "http request", attrs...)
				return nil
			}
			logger.LogAttrs(c.Request().Context(), slog.LevelInfo, "http request", attrs...)
			return nil
		},
	})
}
