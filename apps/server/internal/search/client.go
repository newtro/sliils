package search

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

// Client owns the Meilisearch service handle plus the message index name
// derived from the install-wide index prefix.
//
// Index naming: "<prefix>_messages" — e.g. "sliils_messages" in prod,
// "sliils_dev_messages" in local dev. Letting multiple installs share a
// single Meilisearch process is cheap and makes CI / staging deploys cleaner.
type Client struct {
	svc          meilisearch.ServiceManager
	messageIndex string
	logger       *slog.Logger
}

// ClientOptions captures the startup inputs for NewClient.
type ClientOptions struct {
	URL         string
	MasterKey   string
	IndexPrefix string
	Logger      *slog.Logger
}

// NewClient constructs the service handle. It does not talk to Meili yet —
// call EnsureIndex to perform the bootstrap (index + settings).
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.URL == "" {
		return nil, errors.New("meilisearch url is required")
	}
	if opts.MasterKey == "" {
		return nil, errors.New("meilisearch master key is required")
	}
	if opts.IndexPrefix == "" {
		opts.IndexPrefix = "sliils"
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	svc := meilisearch.New(opts.URL, meilisearch.WithAPIKey(opts.MasterKey))

	return &Client{
		svc:          svc,
		messageIndex: opts.IndexPrefix + "_messages",
		logger:       opts.Logger,
	}, nil
}

// MessageIndex returns the fully-qualified index name Meili keys on.
func (c *Client) MessageIndex() string { return c.messageIndex }

// Svc exposes the underlying service manager for advanced callers (tenant
// token issuance, key lookup).
func (c *Client) Svc() meilisearch.ServiceManager { return c.svc }

// Health checks reachability. Used by /readyz.
func (c *Client) Health(ctx context.Context) error {
	if _, err := c.svc.HealthWithContext(ctx); err != nil {
		return fmt.Errorf("meilisearch health: %w", err)
	}
	return nil
}

// EnsureIndex creates the messages index if missing, then syncs the settings
// we depend on: filterable attributes (drives tenant-token + operator
// filters), searchable attributes (body + channel name), sortable (time),
// and ranking rules (typo-aware default plus recency bias).
//
// Idempotent: applying the same settings is a no-op on Meili's side.
func (c *Client) EnsureIndex(ctx context.Context) error {
	if _, err := c.svc.GetIndexWithContext(ctx, c.messageIndex); err != nil {
		// Create with id as primary key. CreateIndex returns a task;
		// Meili creates indexes lazily too but being explicit is clearer.
		task, err := c.svc.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
			Uid:        c.messageIndex,
			PrimaryKey: "id",
		})
		if err != nil {
			return fmt.Errorf("create index: %w", err)
		}
		if _, err := c.svc.WaitForTaskWithContext(ctx, task.TaskUID, 0); err != nil {
			return fmt.Errorf("wait create index task: %w", err)
		}
		c.logger.Info("meilisearch index created", slog.String("index", c.messageIndex))
	}

	idx := c.svc.Index(c.messageIndex)

	filterable := []interface{}{
		"workspace_id",
		"channel_id",
		"channel_type",
		"channel_member_ids",
		"author_user_id",
		"thread_root_id",
		"has_link",
		"has_file",
		"mention_user_ids",
		"created_at_unix",
	}
	if _, err := idx.UpdateFilterableAttributesWithContext(ctx, &filterable); err != nil {
		return fmt.Errorf("update filterable attrs: %w", err)
	}

	searchable := []string{"body_md", "channel_name"}
	if _, err := idx.UpdateSearchableAttributesWithContext(ctx, &searchable); err != nil {
		return fmt.Errorf("update searchable attrs: %w", err)
	}

	sortable := []string{"created_at_unix"}
	if _, err := idx.UpdateSortableAttributesWithContext(ctx, &sortable); err != nil {
		return fmt.Errorf("update sortable attrs: %w", err)
	}

	c.logger.Info("meilisearch index settings applied", slog.String("index", c.messageIndex))
	return nil
}

// GetOrCreateSearchKey returns an API key configured for search-only access
// to the messages index. Its UID is the apiKeyUid claim that every tenant
// token references; its Key field is used as the HMAC signing secret by
// GenerateTenantToken. We never expose either to clients — tenant tokens
// themselves are scoped, short-lived, and self-validated by Meilisearch.
//
// If a search key with the expected name already exists it is returned as-is;
// otherwise we create one. Named keys keep operators from losing track of
// which secret is in use when rotating.
func (c *Client) GetOrCreateSearchKey(ctx context.Context, name string) (*meilisearch.Key, error) {
	existing, err := c.svc.GetKeysWithContext(ctx, &meilisearch.KeysQuery{Limit: 100})
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	for i := range existing.Results {
		k := existing.Results[i]
		if k.Name == name {
			return &k, nil
		}
	}

	req := &meilisearch.Key{
		Name:        name,
		Description: "SliilS tenant-token parent key — search only",
		Actions:     []string{"search"},
		Indexes:     []string{c.messageIndex},
	}
	created, err := c.svc.CreateKeyWithContext(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create search key: %w", err)
	}
	c.logger.Info("meilisearch search key provisioned",
		slog.String("name", created.Name),
		slog.String("uid", created.UID),
	)
	return created, nil
}

// BuildFilter assembles a Meilisearch filter expression from a list of
// clauses, joining with AND. Empty clauses are dropped. Exposed separately
// so the parser and tenant-token paths can share one serializer.
func BuildFilter(clauses ...string) string {
	out := make([]string, 0, len(clauses))
	for _, c := range clauses {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		out = append(out, "("+c+")")
	}
	return strings.Join(out, " AND ")
}
