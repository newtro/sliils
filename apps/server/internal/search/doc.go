// Package search owns Meilisearch integration for SliilS (M6).
//
// Moving parts:
//
//   - client.go — thin wrapper around meilisearch-go. Owns the server index
//     handle and bootstraps filterable attributes / searchable attributes /
//     sortable fields. One index per install (prefix + "messages").
//
//   - document.go — the shape of the document we push to Meili. Driven by
//     the membership table: public channels get a marker; private channels
//     and DMs carry the full member-id list so the tenant filter works.
//
//   - parser.go — parses operator queries like
//     `from:@alice has:link in:#design urgent roadmap` into a structured
//     QuerySpec. Operators and free text may interleave.
//
//   - tokens.go — issues Meilisearch tenant tokens for a given (workspace,
//     user). Tenant tokens are HMAC-signed JWTs Meili itself validates;
//     they bake the visibility filter into the searchRules so a compromised
//     client can never bypass it.
//
//   - indexer.go — pulls batches from search_outbox, hydrates each row
//     against the owner pool, and reflects the result into Meilisearch.
//     Called from the River periodic worker.
//
//   - service.go — the top-level surface used by HTTP handlers. Owns the
//     client, the tenant-key UID for token signing, and the worker-facing
//     indexer entrypoints.
package search
