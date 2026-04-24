package storage_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/storage"
)

func TestLocalStoragePutGet(t *testing.T) {
	s, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, s.Put(ctx, "ws/1/abc", bytes.NewReader([]byte("hello")), 5, "text/plain"))

	r, err := s.Get(ctx, "ws/1/abc")
	require.NoError(t, err)
	defer r.Close()
	got, _ := io.ReadAll(r)
	assert.Equal(t, "hello", string(got))
}

func TestLocalStorageExists(t *testing.T) {
	s, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	exists, err := s.Exists(ctx, "missing/key")
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, s.Put(ctx, "present/key", bytes.NewReader([]byte("x")), 1, ""))
	exists, err = s.Exists(ctx, "present/key")
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestLocalStorageRejectsTraversal(t *testing.T) {
	s, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	err = s.Put(ctx, "../escape", bytes.NewReader([]byte("x")), 1, "")
	assert.Error(t, err, "must reject paths that escape root")
}

func TestLocalStorageDeleteIsIdempotent(t *testing.T) {
	s, err := storage.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	ctx := context.Background()
	assert.NoError(t, s.Delete(ctx, "never/existed"))
}
