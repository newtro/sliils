package auth_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/auth"
)

func TestHashAndVerifyRoundTrip(t *testing.T) {
	h := auth.NewDefaultHasher()
	// Use cheap params in tests so CI doesn't spend 30s on argon2.
	h.Params.Memory = 8 * 1024
	h.Params.Iterations = 1
	h.Params.Parallelism = 1

	hash, err := h.Hash("correct horse battery staple")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hash, "$argon2id$"))

	ok, err := h.Verify("correct horse battery staple", hash)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = h.Verify("wrong", hash)
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestHashRejectsEmptyPassword(t *testing.T) {
	h := auth.NewDefaultHasher()
	_, err := h.Hash("")
	assert.Error(t, err)
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	h := auth.NewDefaultHasher()
	_, err := h.Verify("anything", "not-a-real-hash")
	assert.Error(t, err)
}

func TestVerifyEmptyInputsFalse(t *testing.T) {
	h := auth.NewDefaultHasher()
	ok, err := h.Verify("", "$argon2id$v=19$m=8192,t=1,p=1$YWFh$YmJi")
	require.NoError(t, err)
	assert.False(t, ok)
}
