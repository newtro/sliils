package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrInvalidToken is returned when a JWT fails validation for any reason.
	// We deliberately collapse signature/expiry/malformed into one error so
	// callers don't accidentally leak validation detail in responses.
	ErrInvalidToken = errors.New("invalid access token")
)

// Claims is the JWT body SliilS signs for every authenticated client.
// `sub` is the user id, `ws` is the currently-selected workspace (nullable;
// populated after M2), `sid` is the server-side session id so revocation is
// possible.
type Claims struct {
	UserID      int64 `json:"sub,string"`
	WorkspaceID int64 `json:"ws,omitempty"`
	SessionID   int64 `json:"sid,string"`
	jwt.RegisteredClaims
}

type TokenIssuer struct {
	signingKey []byte
	ttl        time.Duration
	// Issuer/audience values could become config later. For now we pin them so
	// tokens issued by different installs fail cross-validation if someone
	// tries to reuse them.
	issuer   string
	audience string
}

func NewTokenIssuer(signingKey []byte, ttl time.Duration) *TokenIssuer {
	return &TokenIssuer{
		signingKey: signingKey,
		ttl:        ttl,
		issuer:     "sliils",
		audience:   "sliils-api",
	}
}

// Issue builds a signed access token. workspaceID may be zero (no workspace
// selected yet — pre-M2 or a cross-workspace endpoint).
func (t *TokenIssuer) Issue(userID, sessionID, workspaceID int64) (string, time.Time, error) {
	if len(t.signingKey) == 0 {
		return "", time.Time{}, errors.New("token issuer: signing key not configured")
	}
	now := time.Now().UTC()
	expiry := now.Add(t.ttl)
	claims := Claims{
		UserID:      userID,
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    t.issuer,
			Audience:  jwt.ClaimStrings{t.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiry),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(t.signingKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign: %w", err)
	}
	return signed, expiry, nil
}

// Parse validates the JWT and returns its claims. Any failure collapses to
// ErrInvalidToken so callers can't accidentally leak whether the token was
// malformed vs expired vs signed with a different key.
func (t *TokenIssuer) Parse(raw string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(raw, &c,
		func(tok *jwt.Token) (any, error) {
			if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", tok.Header["alg"])
			}
			return t.signingKey, nil
		},
		jwt.WithIssuer(t.issuer),
		jwt.WithAudience(t.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrInvalidToken
	}
	return &c, nil
}
