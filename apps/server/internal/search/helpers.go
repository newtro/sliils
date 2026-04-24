package search

import (
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
)

// decodeRawInto decodes a json.RawMessage (or raw []byte) into the given
// pointer target. Helper for picking specific fields out of Meili's
// _formatted hit field without defining intermediate structs everywhere.
func decodeRawInto(raw json.RawMessage, out interface{}) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// pgxBeginReadOnly returns the TxOptions used by read-only hydration. Kept
// separate so callers don't have to import pgx/v5 just to set this flag.
func pgxBeginReadOnly() pgx.TxOptions {
	return pgx.TxOptions{AccessMode: pgx.ReadOnly}
}

// lowercaseAll returns a new slice with every entry lowered. Used to feed
// the case-insensitive user lookup without mutating the caller's input.
func lowercaseAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}
