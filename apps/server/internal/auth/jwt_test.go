package auth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sliils/sliils/apps/server/internal/auth"
)

func TestIssueParseRoundTrip(t *testing.T) {
	issuer := auth.NewTokenIssuer([]byte("test-key-0123456789abcdef0123456789abcdef"), 15*time.Minute)

	tok, exp, err := issuer.Issue(42, 99, 7)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), exp, 2*time.Second)

	claims, err := issuer.Parse(tok)
	require.NoError(t, err)
	assert.Equal(t, int64(42), claims.UserID)
	assert.Equal(t, int64(99), claims.SessionID)
	assert.Equal(t, int64(7), claims.WorkspaceID)
}

func TestParseRejectsExpiredToken(t *testing.T) {
	issuer := auth.NewTokenIssuer([]byte("test-key-0123456789abcdef0123456789abcdef"), -1*time.Hour)
	tok, _, err := issuer.Issue(1, 1, 0)
	require.NoError(t, err)

	_, err = issuer.Parse(tok)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestParseRejectsWrongKey(t *testing.T) {
	issuerA := auth.NewTokenIssuer([]byte("key-a-0123456789abcdef0123456789abcdef"), 1*time.Hour)
	issuerB := auth.NewTokenIssuer([]byte("key-b-0123456789abcdef0123456789abcdef"), 1*time.Hour)
	tok, _, err := issuerA.Issue(1, 1, 0)
	require.NoError(t, err)

	_, err = issuerB.Parse(tok)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestParseRejectsGarbage(t *testing.T) {
	issuer := auth.NewTokenIssuer([]byte("test-key-0123456789abcdef0123456789abcdef"), 1*time.Hour)
	_, err := issuer.Parse("not.a.jwt")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestIssueWithoutKeyErrors(t *testing.T) {
	issuer := auth.NewTokenIssuer(nil, 15*time.Minute)
	_, _, err := issuer.Issue(1, 1, 0)
	assert.Error(t, err)
}
