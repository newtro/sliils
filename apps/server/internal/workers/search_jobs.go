package workers

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/sliils/sliils/apps/server/internal/search"
)

// ---- SearchDrainWorker ---------------------------------------------------

// SearchDrainArgs is the periodic-job payload that triggers one outbox
// drain tick. No fields — the drain scans the whole pending queue each run.
type SearchDrainArgs struct{}

func (SearchDrainArgs) Kind() string { return "search.drain" }

// SearchDrainWorker runs one iteration of the outbox-to-Meilisearch pump
// every time the River periodic scheduler fires. Idempotent: a missed tick
// is fine because the next tick claims whatever piled up.
//
// The worker never retries on error — it just logs. The outbox's own
// attempt counter carries the retry semantics (MaxAttempts in IndexerOptions),
// and the next tick will re-claim any row whose lock expires.
type SearchDrainWorker struct {
	river.WorkerDefaults[SearchDrainArgs]
	indexer *search.Indexer
	logger  *slog.Logger
}

func (w *SearchDrainWorker) Work(ctx context.Context, job *river.Job[SearchDrainArgs]) error {
	stats, err := w.indexer.Drain(ctx)
	if err != nil {
		w.logger.Warn("drain error",
			slog.String("error", err.Error()),
			slog.Any("stats", stats),
		)
		return nil // swallow — next tick will retry
	}
	if stats.Claimed > 0 {
		w.logger.Debug("drain tick",
			slog.Int("claimed", stats.Claimed),
			slog.Int("indexed", stats.Indexed),
			slog.Int("deleted", stats.Deleted),
			slog.Int("failed", stats.Failed),
			slog.Int64("pending", stats.Pending),
			slog.Int64("elapsed_ms", stats.ElapsedMS),
		)
	}
	return nil
}

// ---- SearchPruneWorker ---------------------------------------------------

// SearchPruneArgs triggers retention-window pruning of processed outbox rows.
type SearchPruneArgs struct{}

func (SearchPruneArgs) Kind() string { return "search.prune" }

// SearchPruneWorker is the hourly housekeeping job. Cheap — one DELETE.
type SearchPruneWorker struct {
	river.WorkerDefaults[SearchPruneArgs]
	indexer *search.Indexer
	logger  *slog.Logger
}

func (w *SearchPruneWorker) Work(ctx context.Context, job *river.Job[SearchPruneArgs]) error {
	if err := w.indexer.Prune(ctx); err != nil {
		w.logger.Warn("prune error", slog.String("error", err.Error()))
	}
	return nil
}
