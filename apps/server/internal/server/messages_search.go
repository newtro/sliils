package server

import (
	"context"

	"github.com/sliils/sliils/apps/server/internal/db"
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
)

// enqueueSearchIndex records an index-or-update job for a message in the
// search outbox. Called inside the caller's transaction so the outbox entry
// and the message write commit atomically — a crashed server can never leave
// the index and the DB in different states.
//
// payload is kept minimal here; the drain worker re-reads the message at
// process time to pick up the latest body / edits without relying on a
// stale snapshot.
func enqueueSearchIndex(ctx context.Context, scope db.TxScope, workspaceID, messageID int64) error {
	return scope.Queries.EnqueueSearchOutbox(ctx, sqlcgen.EnqueueSearchOutboxParams{
		WorkspaceID: workspaceID,
		Kind:        "message",
		Action:      "index",
		TargetID:    messageID,
		Payload:     []byte(`{}`),
	})
}

// enqueueSearchDelete records a tombstone for a message in the search outbox.
// The drain worker turns this into a Meilisearch DeleteDocuments call.
func enqueueSearchDelete(ctx context.Context, scope db.TxScope, workspaceID, messageID int64) error {
	return scope.Queries.EnqueueSearchOutbox(ctx, sqlcgen.EnqueueSearchOutboxParams{
		WorkspaceID: workspaceID,
		Kind:        "message",
		Action:      "delete",
		TargetID:    messageID,
		Payload:     []byte(`{}`),
	})
}
