// Package main is the entrypoint for the SliilS application server.
//
// The single binary supports multiple modes via --mode:
//   - all    (default): HTTP + WebSocket gateway + worker
//   - app:   HTTP + WebSocket only
//   - worker: River worker only
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/sliils/sliils/apps/server/internal/calls"
	"github.com/sliils/sliils/apps/server/internal/calsync"
	"github.com/sliils/sliils/apps/server/internal/config"
	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/logging"
	"github.com/sliils/sliils/apps/server/internal/pages"
	"github.com/sliils/sliils/apps/server/internal/push"
	"github.com/sliils/sliils/apps/server/internal/search"
	"github.com/sliils/sliils/apps/server/internal/server"
	"github.com/sliils/sliils/apps/server/internal/workers"
	"github.com/sliils/sliils/apps/server/migrations"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	// One-shot helper: `sliils-app genvapid` prints a fresh VAPID keypair
	// and exits. Handy for local dev since the private key must be set in
	// the env before the server starts the push service. The Public key is
	// safe to commit + ship in the client bundle.
	if len(os.Args) >= 2 && os.Args[1] == "genvapid" {
		priv, pub, err := push.GenerateVAPIDKeys()
		if err != nil {
			fmt.Fprintf(os.Stderr, "genvapid: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("SLIILS_VAPID_PUBLIC_KEY=" + pub)
		fmt.Println("SLIILS_VAPID_PRIVATE_KEY=" + priv)
		return
	}

	mode := flag.String("mode", "all", "process mode: all | app | worker")
	flag.Parse()

	if err := run(*mode); err != nil {
		fmt.Fprintf(os.Stderr, "sliils-app: %v\n", err)
		os.Exit(1)
	}
}

func run(mode string) error {
	// Load .env for local development. Non-fatal: production deploys inject
	// env vars via Docker/K8s and have no .env file on disk.
	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	logger.Info("sliils-app starting",
		slog.String("version", version),
		slog.String("mode", mode),
		slog.String("listen", cfg.ListenAddr),
	)

	switch mode {
	case "all", "app":
		return runServer(cfg, logger)
	case "worker":
		return errors.New("worker-only mode not yet implemented (M6 ships workers in-process via --mode=all)")
	default:
		return fmt.Errorf("unknown --mode=%q (want all|app|worker)", mode)
	}
}

func runServer(cfg *config.Config, logger *slog.Logger) error {
	ctx := context.Background()

	// Migrations first: they run under the DSN's original role (not sliils_app)
	// because the first migration creates that role and the rest of the schema
	// is owned by the migration user.
	if cfg.AutoMigrate {
		if err := db.RunMigrations(ctx, cfg.DatabaseURL, migrations.FS, ".", logger); err != nil {
			return fmt.Errorf("run migrations: %w", err)
		}
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL, logger)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	var ownerPool *pgxpool.Pool
	var searchClient *search.Client
	var indexer *search.Indexer
	var tokens *search.TokenIssuer
	var runner *workers.Runner

	// Owner pool is opened whenever any subsystem that needs to bypass
	// RLS is enabled: search drain, calendar sync workers, or pages
	// (snapshot worker + comment-by-id lookup). River migrations run
	// against the owner pool too since River tables live there.
	needsOwnerPool := cfg.SearchEnabled || cfg.PagesEnabled || cfg.ExternalCalendarsEnabled || cfg.PushEnabled
	if needsOwnerPool {
		op, err := db.OpenOwner(ctx, cfg.DatabaseURL, logger)
		if err != nil {
			return fmt.Errorf("open owner pool: %w", err)
		}
		ownerPool = op
		defer ownerPool.Close()

		if err := workers.RunMigrations(ctx, ownerPool, logger); err != nil {
			return fmt.Errorf("river migrate: %w", err)
		}
	}

	if cfg.SearchEnabled {
		sc, err := search.NewClient(search.ClientOptions{
			URL:         cfg.MeiliURL,
			MasterKey:   cfg.MeiliMasterKey,
			IndexPrefix: cfg.SearchIndexPrefix,
			Logger:      logger,
		})
		if err != nil {
			return fmt.Errorf("init search client: %w", err)
		}
		if err := sc.Health(ctx); err != nil {
			logger.Warn("meilisearch unreachable at startup; search will retry", slog.String("error", err.Error()))
		}
		if err := sc.EnsureIndex(ctx); err != nil {
			logger.Warn("meilisearch index bootstrap failed", slog.String("error", err.Error()))
		}
		searchClient = sc

		parentKey, err := sc.GetOrCreateSearchKey(ctx, "sliils-tenant-token-parent")
		if err != nil {
			logger.Warn("could not provision meilisearch tenant key; token issuance disabled",
				slog.String("error", err.Error()))
		} else {
			issuer, err := search.NewTokenIssuer(sc, parentKey)
			if err != nil {
				logger.Warn("tenant-token issuer init failed", slog.String("error", err.Error()))
			} else {
				tokens = issuer
			}
		}

		indexer = search.NewIndexer(sc, ownerPool, logger, search.IndexerOptions{})
		// The runner itself is constructed further below, after we
		// build the Server (so calendar reminders can share its broker).
	}

	var callsClient *calls.Client
	if cfg.CallsEnabled {
		cc, err := calls.NewClient(calls.Options{
			APIKey:    cfg.LiveKitAPIKey,
			APISecret: cfg.LiveKitAPISecret,
			HTTPURL:   cfg.LiveKitURL,
			WSURL:     cfg.LiveKitWSURL,
			Logger:    logger,
		})
		if err != nil {
			return fmt.Errorf("init livekit client: %w", err)
		}
		// A health probe at startup gives a clear boot log line for dev
		// workflow; we don't fail startup if LiveKit's down since the
		// dev loop typically brings services up in any order.
		if err := cc.Health(ctx); err != nil {
			logger.Warn("livekit unreachable at startup; calls will retry",
				slog.String("error", err.Error()))
		} else {
			logger.Info("livekit reachable", slog.String("ws_url", cfg.LiveKitWSURL))
		}
		callsClient = cc
	}

	var ySweet *pages.Client
	if cfg.PagesEnabled {
		yc, err := pages.NewClient(pages.Options{
			BaseURL:     cfg.YSweetURL,
			ServerToken: cfg.YSweetServerToken,
			Logger:      logger,
		})
		if err != nil {
			return fmt.Errorf("init y-sweet client: %w", err)
		}
		// Health probe is advisory — pages work without a live Y-Sweet
		// for read-only ops like listing + comments. Create / join
		// return 503 gracefully when Y-Sweet is unreachable.
		if err := yc.Health(ctx); err != nil {
			logger.Warn("y-sweet unreachable at startup; pages sync will retry",
				slog.String("error", err.Error()))
		} else {
			logger.Info("y-sweet reachable", slog.String("url", cfg.YSweetURL))
		}
		ySweet = yc
	}

	var pushSvc *push.Service
	if cfg.PushEnabled {
		var proxy *push.ProxyConfig
		if cfg.PushProxyURL != "" {
			proxy = &push.ProxyConfig{
				URL:        cfg.PushProxyURL,
				InstallJWT: cfg.PushProxyJWT,
				TenantID:   cfg.PushTenantID,
			}
		}
		ps, err := push.New(push.Options{
			VAPIDPublicKey:  cfg.VAPIDPublicKey,
			VAPIDPrivateKey: cfg.VAPIDPrivateKey,
			Subject:         cfg.VAPIDSubject,
			TTLSeconds:      cfg.PushTTLSeconds,
			Proxy:           proxy,
			Logger:          logger,
		})
		if err != nil {
			return fmt.Errorf("init push service: %w", err)
		}
		if cfg.VAPIDPublicKey == "" || cfg.VAPIDPrivateKey == "" {
			logger.Warn("push enabled but VAPID keys are empty — web push will fail until SLIILS_VAPID_PUBLIC_KEY / SLIILS_VAPID_PRIVATE_KEY are set; generate with `go run ./cmd/sliils-app genvapid`")
		} else {
			logger.Info("push service ready", slog.Bool("proxy_configured", proxy != nil))
		}
		pushSvc = ps
	}

	var calSync *calsync.Service
	if cfg.ExternalCalendarsEnabled {
		svc, err := calsync.NewService(calsync.Options{
			EncryptionKey:         cfg.CalendarEncryptionKey,
			StateHMACKey:          []byte(cfg.JWTSigningKey),
			GoogleClientID:        cfg.GoogleOAuthClientID,
			GoogleClientSecret:    cfg.GoogleOAuthClientSecret,
			GoogleRedirectURL:     cfg.GoogleOAuthRedirectURL,
			MicrosoftClientID:     cfg.MicrosoftOAuthClientID,
			MicrosoftClientSecret: cfg.MicrosoftOAuthClientSecret,
			MicrosoftRedirectURL:  cfg.MicrosoftOAuthRedirectURL,
		})
		if err != nil {
			return fmt.Errorf("init calendar sync: %w", err)
		}
		calSync = svc
		logger.Info("external calendar sync enabled", slog.Int("providers", len(svc.Providers())))
	}

	srv, err := server.New(cfg, logger, pool, server.Options{
		SearchClient:  searchClient,
		SearchTokens:  tokens,
		SearchOwnerDB: ownerPool,
		CallsClient:   callsClient,
		CalSync:       calSync,
		YSweet:        ySweet,
		Push:          pushSvc,
	})
	if err != nil {
		return fmt.Errorf("build server: %w", err)
	}

	// Build the worker runner after the server so calendar reminders can
	// publish on the server's broker. The runner needs the owner pool
	// regardless; if search is off we still register the calendar job.
	if ownerPool != nil {
		runner, err = workers.NewRunner(workers.Options{
			OwnerPool:         ownerPool,
			SearchEnabled:     cfg.SearchEnabled,
			Indexer:           indexer,
			SearchDrainPeriod: cfg.SearchDrainPeriod,
			Broker:            srv.Broker(),
			CalSync:           calSync,
			CalSyncPullPeriod: cfg.CalendarSyncPeriod,
			YSweet:            ySweet,
			PageSnapshotPeriod:    cfg.PageSnapshotPeriod,
			PageSnapshotRetention: cfg.PageSnapshotRetention,
			Push:              pushSvc,
			Logger:            logger,
		})
		if err != nil {
			return fmt.Errorf("build worker runner: %w", err)
		}
		// Late-bind the event handlers' push-enqueue hook so CRUD on
		// /events kicks a CalendarPushJob into the River queue. nil-safe
		// when external sync isn't configured.
		if calSync != nil {
			srv.SetCalendarPushEnqueue(runner.EnqueueCalendarPush)
		}
		if pushSvc != nil {
			srv.SetPushEnqueue(runner.EnqueuePushFanout)
		}
	}

	sigCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if runner != nil {
		if err := runner.Start(sigCtx); err != nil {
			return fmt.Errorf("start river runner: %w", err)
		}
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("server exited: %w", err)
	case <-sigCtx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if runner != nil {
		if err := runner.Stop(shutdownCtx); err != nil {
			logger.Warn("river stop error", slog.String("error", err.Error()))
		}
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("sliils-app stopped cleanly")
	return nil
}

// loadDotEnv walks up from the current working directory looking for a .env
// file and loads it. Env vars already set in the real environment take
// precedence — `.env` only fills in the gaps.
func loadDotEnv() {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	// Check cwd and one parent level — handles `go run ./cmd/sliils-app`
	// from apps/server/ and also `go run .` from cmd/sliils-app/.
	candidates := []string{
		filepath.Join(cwd, ".env"),
		filepath.Join(cwd, "..", ".env"),
		filepath.Join(cwd, "..", "..", ".env"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			_ = godotenv.Load(p)
			return
		}
	}
}
