package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
	"github.com/meilisearch/meilisearch-go"
)

// Indexer drains search_outbox and reflects the result into Meilisearch.
//
// Design:
//   - Runs on the owner pool (RLS bypassed) so it can read across every
//     workspace, including private channel membership rows.
//   - Batches up to DrainBatchSize rows per tick. Each tick is one claim +
//     one Meilisearch upsert + one Meilisearch delete per workspace + one
//     outbox-mark-processed.
//   - Hydration always re-reads the message (we don't trust the outbox
//     payload for content). An index event for a deleted-then-restored
//     message still works: we see deleted_at set, fall through to the
//     delete path.
//
// Concurrency: safe for multiple concurrent Drain calls (FOR UPDATE SKIP
// LOCKED). In M6 we only run one drain per process; the property matters
// more for future scale-out.
type Indexer struct {
	client *Client
	pool   *pgxpool.Pool
	q      *sqlcgen.Queries
	logger *slog.Logger
	opts   IndexerOptions
}

// IndexerOptions captures the tunables we expose. All fields have sensible
// defaults; zero values are accepted.
type IndexerOptions struct {
	DrainBatchSize   int           // max outbox rows per Drain call (default 200)
	MaxAttempts      int           // abandon after this many failed tries (default 10)
	PartitionPruneBy time.Duration // how far back to allow indexing (default 365 days)
	PruneAfter       time.Duration // drop processed rows older than this (default 24h)
}

// NewIndexer wires the moving parts. pool must be the owner pool so the
// RLS-sensitive hydration queries can see every tenant.
func NewIndexer(client *Client, pool *pgxpool.Pool, logger *slog.Logger, opts IndexerOptions) *Indexer {
	if opts.DrainBatchSize <= 0 {
		opts.DrainBatchSize = 200
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 10
	}
	if opts.PartitionPruneBy <= 0 {
		opts.PartitionPruneBy = 365 * 24 * time.Hour
	}
	if opts.PruneAfter <= 0 {
		opts.PruneAfter = 24 * time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Indexer{
		client: client,
		pool:   pool,
		q:      sqlcgen.New(pool),
		logger: logger,
		opts:   opts,
	}
}

// DrainStats surfaces what a single Drain call did. Logged at info level;
// tests use it to assert progress without parsing log lines.
type DrainStats struct {
	Claimed   int
	Indexed   int
	Deleted   int
	Failed    int
	Pending   int64 // outbox depth after this tick
	ElapsedMS int64
}

// Drain runs one iteration of the outbox-to-Meili pump. It is safe to call
// from a River periodic job; any residual work will be picked up next tick.
func (ix *Indexer) Drain(ctx context.Context) (*DrainStats, error) {
	start := time.Now()
	stats := &DrainStats{}

	rows, err := ix.q.ClaimSearchOutboxBatch(ctx, sqlcgen.ClaimSearchOutboxBatchParams{
		MaxAttempts: int32(ix.opts.MaxAttempts),
		BatchLimit:  int32(ix.opts.DrainBatchSize),
	})
	if err != nil {
		return nil, fmt.Errorf("claim outbox batch: %w", err)
	}
	stats.Claimed = len(rows)

	if len(rows) == 0 {
		if n, err := ix.q.CountPendingSearchOutbox(ctx); err == nil {
			stats.Pending = n
		}
		stats.ElapsedMS = time.Since(start).Milliseconds()
		return stats, nil
	}

	idx := ix.client.svc.Index(ix.client.messageIndex)
	lowerBound := pgtype.Timestamptz{Time: time.Now().Add(-ix.opts.PartitionPruneBy), Valid: true}

	// Group rows into a single upsert batch + a single delete batch so we
	// issue at most two Meilisearch calls per tick.
	var upserts []MessageDoc
	var deletes []string
	succeeded := make([]int64, 0, len(rows))

	for _, row := range rows {
		switch row.Action {
		case "delete":
			deletes = append(deletes, DocID(row.TargetID))
			succeeded = append(succeeded, row.ID)
			stats.Deleted++
		case "index":
			doc, ok, err := ix.buildDoc(ctx, row.TargetID, lowerBound)
			if err != nil {
				ix.logger.Warn("indexer: hydrate failed",
					slog.Int64("outbox_id", row.ID),
					slog.Int64("message_id", row.TargetID),
					slog.String("error", err.Error()),
				)
				ix.recordFailure(ctx, row.ID, err)
				stats.Failed++
				continue
			}
			if !ok {
				// Message vanished — fell out of the partition prune window
				// or was hard-deleted. Treat as a delete so any residual doc
				// is purged.
				deletes = append(deletes, DocID(row.TargetID))
				succeeded = append(succeeded, row.ID)
				stats.Deleted++
				continue
			}
			upserts = append(upserts, doc)
			succeeded = append(succeeded, row.ID)
			stats.Indexed++
		default:
			ix.logger.Warn("indexer: unknown action",
				slog.String("action", row.Action),
				slog.Int64("outbox_id", row.ID),
			)
			ix.recordFailure(ctx, row.ID, fmt.Errorf("unknown action %q", row.Action))
			stats.Failed++
		}
	}

	if len(upserts) > 0 {
		primaryKey := "id"
		if _, err := idx.AddDocumentsWithContext(ctx, upserts, &meilisearch.DocumentOptions{PrimaryKey: &primaryKey}); err != nil {
			// A Meili-wide failure rolls back every success we collected in
			// this tick — we do NOT mark rows processed so they'll be
			// re-claimed next tick.
			ix.logger.Error("indexer: meilisearch upsert failed",
				slog.Int("docs", len(upserts)),
				slog.String("error", err.Error()),
			)
			stats.Failed += len(upserts)
			stats.Indexed -= len(upserts)
			stats.ElapsedMS = time.Since(start).Milliseconds()
			return stats, fmt.Errorf("meili add documents: %w", err)
		}
	}
	if len(deletes) > 0 {
		if _, err := idx.DeleteDocumentsWithContext(ctx, deletes, nil); err != nil {
			ix.logger.Error("indexer: meilisearch delete failed",
				slog.Int("ids", len(deletes)),
				slog.String("error", err.Error()),
			)
			stats.Failed += len(deletes)
			stats.Deleted -= len(deletes)
			stats.ElapsedMS = time.Since(start).Milliseconds()
			return stats, fmt.Errorf("meili delete documents: %w", err)
		}
	}

	if len(succeeded) > 0 {
		if err := ix.q.MarkSearchOutboxProcessed(ctx, succeeded); err != nil {
			// Meili already took the write; on mark-processed failure the
			// rows will be re-claimed and re-pushed. Meili upserts are
			// idempotent (id is primary key) so this is safe.
			return stats, fmt.Errorf("mark outbox processed: %w", err)
		}
	}

	if n, err := ix.q.CountPendingSearchOutbox(ctx); err == nil {
		stats.Pending = n
	}
	stats.ElapsedMS = time.Since(start).Milliseconds()
	return stats, nil
}

// Prune is called from the same periodic job on a lower cadence. Drops
// processed rows older than the retention window.
func (ix *Indexer) Prune(ctx context.Context) error {
	return ix.q.PruneProcessedSearchOutbox(ctx, fmt.Sprintf("%d seconds", int(ix.opts.PruneAfter.Seconds())))
}

// buildDoc hydrates one message id into a Meilisearch document. Returns
// (doc, true, nil) on success; (zero, false, nil) when the message no
// longer exists (hard-deleted or out of prune window); error otherwise.
func (ix *Indexer) buildDoc(ctx context.Context, messageID int64, lowerBound pgtype.Timestamptz) (MessageDoc, bool, error) {
	m, err := ix.q.GetMessageForIndexing(ctx, sqlcgen.GetMessageForIndexingParams{
		ID:        messageID,
		CreatedAt: lowerBound,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MessageDoc{}, false, nil
		}
		return MessageDoc{}, false, err
	}

	// Soft-deleted messages live in the DB but must not be searchable.
	// The caller treats buildDoc's (false, nil) return as "delete from
	// index" so we don't need a separate branch here — just report gone.
	if m.DeletedAt.Valid {
		return MessageDoc{}, false, nil
	}

	channelType := NormalizeChannelType(m.ChannelType)

	var memberIDs []int64
	if channelType != "public" {
		ids, err := ix.q.ListChannelMemberIDs(ctx, m.ChannelID)
		if err != nil {
			return MessageDoc{}, false, fmt.Errorf("list channel members: %w", err)
		}
		memberIDs = ids
	}

	hasFile, err := ix.q.MessageHasAttachments(ctx, messageID)
	if err != nil {
		return MessageDoc{}, false, fmt.Errorf("check attachments: %w", err)
	}

	hasLink, mentions := AnalyzeBody(m.BodyMd)

	doc := MessageDoc{
		ID:               DocID(messageID),
		MessageID:        m.ID,
		WorkspaceID:      m.WorkspaceID,
		ChannelID:        m.ChannelID,
		ChannelType:      channelType,
		ChannelMemberIDs: memberIDs,
		BodyMD:           m.BodyMd,
		HasLink:          hasLink,
		HasFile:          hasFile,
		MentionUserIDs:   mentions,
		CreatedAtUnix:    UnixFromTime(m.CreatedAt.Time),
		CreatedAtISO:     m.CreatedAt.Time.UTC().Format(time.RFC3339),
	}
	if m.AuthorUserID != nil {
		doc.AuthorUserID = *m.AuthorUserID
	}
	if m.ThreadRootID != nil {
		doc.ThreadRootID = *m.ThreadRootID
	}
	if m.ChannelName != nil {
		doc.ChannelName = *m.ChannelName
	}
	return doc, true, nil
}

// recordFailure updates the outbox row with the error text; the attempt
// count is already incremented by ClaimSearchOutboxBatch.
func (ix *Indexer) recordFailure(ctx context.Context, outboxID int64, cause error) {
	msg := cause.Error()
	_ = ix.q.MarkSearchOutboxFailed(ctx, sqlcgen.MarkSearchOutboxFailedParams{
		ID:        outboxID,
		LastError: &msg,
	})
}

// ensureIndex exists here so callers can trigger the bootstrap explicitly at
// startup without importing meilisearch-go themselves. Thin pass-through.
func (ix *Indexer) ensureIndex(ctx context.Context) error {
	return ix.client.EnsureIndex(ctx)
}
