package server

import (
	"github.com/sliils/sliils/apps/server/internal/db/sqlcgen"
)

// userFromRow converts a DB row to the JSON-facing DTO. Centralized so we
// can't accidentally leak password_hash or totp_secret in a handler.
func userFromRow(u *sqlcgen.User) UserDTO {
	dto := UserDTO{
		ID:           u.ID,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		CreatedAt:    u.CreatedAt.Time,
		IsSuperAdmin: u.IsSuperAdmin,
	}
	if u.EmailVerifiedAt.Valid {
		t := u.EmailVerifiedAt.Time
		dto.EmailVerifiedAt = &t
	}
	return dto
}
