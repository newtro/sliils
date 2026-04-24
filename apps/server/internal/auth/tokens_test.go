package auth_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/auth"
)

func TestRandomTokenUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		tok, err := auth.RandomToken(32)
		require.NoError(t, err)
		_, dup := seen[tok]
		assert.False(t, dup, "duplicate token at iteration %d", i)
		seen[tok] = struct{}{}
	}
}

func TestRandomTokenEntropyFloor(t *testing.T) {
	_, err := auth.RandomToken(8)
	assert.Error(t, err)
}

func TestRandomTokenDecodesToExpectedBytes(t *testing.T) {
	tok, err := auth.RandomToken(32)
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	require.NoError(t, err)
	assert.Len(t, raw, 32)
}

func TestHashTokenIsStable(t *testing.T) {
	a := auth.HashToken("hello")
	b := auth.HashToken("hello")
	assert.Equal(t, a, b)
	assert.Len(t, a, 32)
}

func TestHashTokenDiffersPerInput(t *testing.T) {
	a := auth.HashToken("a")
	b := auth.HashToken("b")
	assert.NotEqual(t, a, b)
}
