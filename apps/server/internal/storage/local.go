package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LocalStorage writes blobs to a directory on disk. Sufficient for dev,
// single-node deploys, and test harnesses. Each key becomes a relative
// path under Root.
//
// Keys may contain forward slashes; the driver translates to the host
// path separator. The only restriction is they must not escape Root —
// attempts to write `../foo` are rejected.
type LocalStorage struct {
	Root string
}

func NewLocalStorage(root string) (*LocalStorage, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve storage root: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir storage root: %w", err)
	}
	return &LocalStorage{Root: abs}, nil
}

func (l *LocalStorage) Backend() string { return "local" }

func (l *LocalStorage) Put(ctx context.Context, key string, r io.Reader, _ int64, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dst, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Write to a .tmp sibling and rename on success so we never expose a
	// half-written file to readers.
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func (l *LocalStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (l *LocalStorage) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (l *LocalStorage) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	p, err := l.resolve(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// resolve joins key onto Root and rejects anything that escapes the root
// via `..` traversal. Callers cannot reach outside the configured dir.
func (l *LocalStorage) resolve(key string) (string, error) {
	key = strings.TrimPrefix(key, "/")
	if key == "" {
		return "", errors.New("empty storage key")
	}
	full := filepath.Join(l.Root, filepath.FromSlash(key))
	// filepath.Join cleans ..; verify the result is still under Root.
	rel, err := filepath.Rel(l.Root, full)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("storage key escapes root: %q", key)
	}
	return full, nil
}
