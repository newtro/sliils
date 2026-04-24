// Package storage is the driver seam for blob storage.
//
// At M5 only LocalStorage is implemented. The S3Storage driver (for
// SeaweedFS, R2, B2, Wasabi, Garage, and any other S3-compatible backend)
// slots in here at v1's SeaweedFS rollout without touching HTTP handlers.
//
// Keys are opaque to the driver — callers pass the key they want, the
// driver persists it. The handler layer is responsible for choosing keys
// that encode the workspace + sha256 so nothing collides.
package storage

import (
	"context"
	"io"
)

// Store is the abstract interface implemented by each backend driver.
type Store interface {
	// Backend returns the driver id written to files.storage_backend.
	Backend() string

	// Put streams the reader to the given key. The size hint is advisory;
	// drivers that need content-length (S3) read it, drivers that don't
	// (local fs) ignore it.
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error

	// Get opens a reader for the stored bytes. Caller must Close.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Delete removes the object. Idempotent — no error if key was absent.
	Delete(ctx context.Context, key string) error

	// Exists returns true if the key is present. Cheap probe before Put
	// for idempotent re-uploads.
	Exists(ctx context.Context, key string) (bool, error)
}
