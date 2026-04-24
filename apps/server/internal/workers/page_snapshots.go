package workers

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/sliils/sliils/apps/server/internal/pages"
)

// Page snapshot worker (M10-P3).
//
// Fires on a periodic cadence (default 5 minutes). For every non-archived
// page that has been touched since its last snapshot, we grab the current
// Y-Sweet state and persist it as a new row in page_snapshots. Old rows
// beyond the per-page retention are pruned in the same tx.
//
// Why drive this from a worker instead of the HTTP handler?
//   Clients don't natively know when a doc has been edited enough to
//   warrant a checkpoint. A server-side timer is cheap, idempotent, and
//   gives us a clean fallback if the Y-Sweet process restarts.

type PageSnapshotArgs struct{}

func (PageSnapshotArgs) Kind() string { return "pages.snapshot_sweep" }

type PageSnapshotWorker struct {
	river.WorkerDefaults[PageSnapshotArgs]
	pool      *pgxpool.Pool
	ySweet    *pages.Client
	retention int
	logger    *slog.Logger
}

func (w *PageSnapshotWorker) Work(ctx context.Context, job *river.Job[PageSnapshotArgs]) error {
	// Find pages that have activity since the last snapshot. We scope to
	// a modest batch per tick so a backlog can't monopolise the worker.
	rows, err := w.pool.Query(ctx, `
		SELECT p.id, p.workspace_id, p.doc_id
		FROM pages p
		LEFT JOIN LATERAL (
			SELECT MAX(created_at) AS max_at
			FROM page_snapshots s
			WHERE s.page_id = p.id
		) s ON true
		WHERE p.archived_at IS NULL
		  AND (s.max_at IS NULL OR p.updated_at > s.max_at)
		ORDER BY p.updated_at ASC
		LIMIT 50
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type target struct {
		id          int64
		workspaceID int64
		docID       string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.workspaceID, &t.docID); err != nil {
			return err
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	q := sqlcgen.New(w.pool)
	for _, t := range targets {
		snapCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		data, err := w.ySweet.GetSnapshot(snapCtx, t.docID)
		cancel()
		if err != nil {
			w.logger.Warn("snapshot: fetch state failed",
				slog.Int64("page_id", t.id),
				slog.String("error", err.Error()))
			continue
		}
		if len(data) == 0 {
			// Nothing persisted yet on the Y-Sweet side (e.g. a brand-new
			// doc whose first update hasn't landed). Skip silently.
			continue
		}
		if _, err := q.CreatePageSnapshot(ctx, sqlcgen.CreatePageSnapshotParams{
			PageID:       t.id,
			WorkspaceID:  t.workspaceID,
			SnapshotData: data,
			ByteSize:     int32(len(data)),
			CreatedBy:    nil,
			Reason:       "periodic",
		}); err != nil {
			w.logger.Warn("snapshot: insert failed",
				slog.Int64("page_id", t.id),
				slog.String("error", err.Error()))
			continue
		}
		if err := q.PruneOldSnapshots(ctx, sqlcgen.PruneOldSnapshotsParams{
			PageID: t.id,
			Offset: int32(w.retention),
		}); err != nil {
			w.logger.Warn("snapshot: prune failed",
				slog.Int64("page_id", t.id),
				slog.String("error", err.Error()))
		}
	}
	return nil
}
