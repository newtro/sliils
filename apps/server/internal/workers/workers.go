// Package workers hosts River-based background job runners for SliilS.
//
// M6 introduces this package solely to drain the search_outbox into
// Meilisearch. Future milestones add clamd (M5.1 deferred), thumbnail /
// video derivative jobs (M5.1 deferred), calendar sync (M9), etc.
//
// Layering:
//
//	main / server.New
//	  └── workers.NewRunner(...)          // wires River + jobs to deps
//	        ├── SearchDrainWorker          // drains search_outbox
//	        └── (future: AVScanWorker, DerivativeWorker, ...)
//
// The runner owns the River client and starts / stops it with the server.
package workers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/sliils/sliils/apps/server/internal/calsync"
	"github.com/sliils/sliils/apps/server/internal/pages"
	"github.com/sliils/sliils/apps/server/internal/realtime"
	"github.com/sliils/sliils/apps/server/internal/search"
)

// Runner bundles the River client and whatever dependencies its registered
// workers need. Start/Stop mirror the rest of the server's lifecycle.
type Runner struct {
	rc     *river.Client[pgx.Tx]
	logger *slog.Logger
}

// Options collects startup inputs.
type Options struct {
	// OwnerPool is the privileged Postgres pool used by River for its own
	// tables (river_job, etc.) and by workers for cross-tenant work. It
	// must connect as the DSN owner — NOT as sliils_app — so River's DDL
	// migrations and the search indexer's hydration queries have the
	// permissions they need.
	OwnerPool *pgxpool.Pool

	// SearchEnabled controls whether the search-drain job is registered.
	// When false, NewRunner still produces a runner so future workers can
	// use River, but the drain is skipped entirely.
	SearchEnabled bool

	// Indexer is the drain implementation the SearchDrainWorker calls. May
	// be nil when SearchEnabled is false.
	Indexer *search.Indexer

	// SearchDrainPeriod is how often the drain fires. 2s is a good default
	// well under the 60s delete-purge SLO mandated in M6 acceptance.
	SearchDrainPeriod time.Duration

	// Broker is the in-process realtime broker that workers publish
	// events into (calendar reminders, future push-notification fanout).
	Broker *realtime.Broker

	// CalSync wires the calendar-sync workers (push/pull). Nil = external
	// calendar sync disabled; those workers aren't registered.
	CalSync *calsync.Service

	// CalSyncPullPeriod controls how often the pull worker fires.
	// Defaults to 60s — matches the M9 acceptance gate.
	CalSyncPullPeriod time.Duration

	// YSweet wires the page-snapshot worker. Nil = pages disabled; the
	// worker is skipped.
	YSweet *pages.Client

	// PageSnapshotPeriod controls how often the snapshot sweep fires.
	// Defaults to 5 minutes.
	PageSnapshotPeriod time.Duration

	// PageSnapshotRetention caps the number of snapshots kept per page.
	// Defaults to 50.
	PageSnapshotRetention int

	// Logger is used for worker diagnostics. Falls back to slog.Default.
	Logger *slog.Logger
}

// NewRunner wires the River client and registers every worker whose deps are
// present. Callers are responsible for running RunMigrations (River's
// schema) before Start; see Runner.RunMigrations.
func NewRunner(opts Options) (*Runner, error) {
	if opts.OwnerPool == nil {
		return nil, fmt.Errorf("owner pool is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.SearchDrainPeriod <= 0 {
		opts.SearchDrainPeriod = 2 * time.Second
	}

	workers := river.NewWorkers()

	var periodicJobs []*river.PeriodicJob

	if opts.SearchEnabled {
		if opts.Indexer == nil {
			return nil, fmt.Errorf("indexer is required when search is enabled")
		}
		river.AddWorker(workers, &SearchDrainWorker{
			indexer: opts.Indexer,
			logger:  opts.Logger.With(slog.String("worker", "search_drain")),
		})

		periodicJobs = append(periodicJobs,
			river.NewPeriodicJob(
				river.PeriodicInterval(opts.SearchDrainPeriod),
				func() (river.JobArgs, *river.InsertOpts) {
					return SearchDrainArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
			// Hourly prune of processed rows. Cheap; runs the retention
			// sweep without extra coordination.
			river.NewPeriodicJob(
				river.PeriodicInterval(1*time.Hour),
				func() (river.JobArgs, *river.InsertOpts) {
					return SearchPruneArgs{}, nil
				},
				nil,
			),
		)

		river.AddWorker(workers, &SearchPruneWorker{
			indexer: opts.Indexer,
			logger:  opts.Logger.With(slog.String("worker", "search_prune")),
		})
	}

	// Calendar event reminders (M9-P2). Fire once a minute whenever the
	// broker is wired up. Idempotent enough that we don't need a separate
	// flag — when no events match the lead-time window the worker no-ops.
	if opts.Broker != nil {
		river.AddWorker(workers, &EventReminderWorker{
			pool:   opts.OwnerPool,
			broker: opts.Broker,
			logger: opts.Logger.With(slog.String("worker", "event_reminders")),
		})
		periodicJobs = append(periodicJobs,
			river.NewPeriodicJob(
				river.PeriodicInterval(1*time.Minute),
				func() (river.JobArgs, *river.InsertOpts) {
					return EventReminderArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		)
	}

	// External calendar sync (M9-P3). Push is an on-demand job queued by
	// the events handler; pull is periodic.
	if opts.CalSync != nil {
		river.AddWorker(workers, &CalendarPushWorker{
			pool:    opts.OwnerPool,
			calSync: opts.CalSync,
			logger:  opts.Logger.With(slog.String("worker", "calendar_push")),
		})
		river.AddWorker(workers, &CalendarPullWorker{
			pool:    opts.OwnerPool,
			calSync: opts.CalSync,
			logger:  opts.Logger.With(slog.String("worker", "calendar_pull")),
		})
		pullInterval := opts.CalSyncPullPeriod
		if pullInterval <= 0 {
			pullInterval = 60 * time.Second
		}
		periodicJobs = append(periodicJobs,
			river.NewPeriodicJob(
				river.PeriodicInterval(pullInterval),
				func() (river.JobArgs, *river.InsertOpts) {
					return CalendarPullArgs{}, nil
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
		)
	}

	// Page snapshots (M10-P3). Runs whenever Y-Sweet is wired; idempotent
	// and no-op when no pages have activity.
	if opts.YSweet != nil {
		retention := opts.PageSnapshotRetention
		if retention < 1 {
			retention = 50
		}
		period := opts.PageSnapshotPeriod
		if period <= 0 {
			period = 5 * time.Minute
		}
		river.AddWorker(workers, &PageSnapshotWorker{
			pool:      opts.OwnerPool,
			ySweet:    opts.YSweet,
			retention: retention,
			logger:    opts.Logger.With(slog.String("worker", "page_snapshots")),
		})
		periodicJobs = append(periodicJobs,
			river.NewPeriodicJob(
				river.PeriodicInterval(period),
				func() (river.JobArgs, *river.InsertOpts) {
					return PageSnapshotArgs{}, nil
				},
				nil,
			),
		)
	}

	rc, err := river.NewClient(riverpgxv5.New(opts.OwnerPool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 4},
		},
		Workers:      workers,
		PeriodicJobs: periodicJobs,
		Logger:       opts.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("new river client: %w", err)
	}

	return &Runner{rc: rc, logger: opts.Logger}, nil
}

// RunMigrations applies River's own schema to the owner pool. Safe to run at
// every startup — River is idempotent about its migrations.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("new river migrator: %w", err)
	}
	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}
	for _, v := range res.Versions {
		logger.Info("river migration applied",
			slog.Int("version", v.Version),
			slog.String("direction", string(res.Direction)),
		)
	}
	return nil
}

// Start brings the River client online. Non-blocking — River manages its own
// goroutine.
func (r *Runner) Start(ctx context.Context) error {
	return r.rc.Start(ctx)
}

// Stop drains in-flight jobs and shuts the River client down.
func (r *Runner) Stop(ctx context.Context) error {
	return r.rc.Stop(ctx)
}

// Client exposes the underlying River client for callers that want to
// enqueue jobs (e.g., an explicit "reindex this message now" API endpoint).
// Nil-safe: returns nil when the runner was built without search enabled.
func (r *Runner) Client() *river.Client[pgx.Tx] {
	if r == nil {
		return nil
	}
	return r.rc
}

// EnqueueCalendarPush inserts a CalendarPushArgs job into the queue.
// Used by HTTP handlers via server.SetCalendarPushEnqueue. The returned
// InsertResult is dropped on the floor — the caller only cares about
// success/failure.
func (r *Runner) EnqueueCalendarPush(ctx context.Context, eventID, userID int64, action string) error {
	if r == nil || r.rc == nil {
		return nil
	}
	_, err := r.rc.Insert(ctx, CalendarPushArgs{
		EventID: eventID,
		UserID:  userID,
		Action:  action,
	}, nil)
	return err
}
