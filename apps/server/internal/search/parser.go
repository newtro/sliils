package search

import (
	"strings"
)

// QuerySpec is the parsed form of a user-typed search query.
//
// The text the user sees looks like:
//
//	from:@alice has:link in:#design urgent roadmap
//
// which becomes:
//
//	QuerySpec{
//	    Text:        "urgent roadmap",
//	    From:        []string{"alice"},
//	    InChannels:  []string{"design"},
//	    HasLink:     true,
//	}
//
// Operators are case-insensitive and may appear in any order; unknown
// operators are left in Text so users don't have their typed text silently
// dropped. A plain word with no colon is always free-text.
//
// The parser deliberately does not resolve names to ids — that happens later
// in the service when it has access to the DB. Keeping ParseQuery pure lets
// us unit-test the parser without any fixture or fake.
type QuerySpec struct {
	Text       string   // the free-text portion, space-joined in original order
	From       []string // usernames after `from:@`, without the `@`
	InChannels []string // channel names after `in:#`, without the `#`
	HasLink    bool     // `has:link`
	HasFile    bool     // `has:file`
	Mentions   []string // usernames after `mentions:@` (who was pinged)
}

// ParseQuery splits a raw query string into operators + free text.
func ParseQuery(raw string) QuerySpec {
	spec := QuerySpec{}
	if strings.TrimSpace(raw) == "" {
		return spec
	}

	fields := strings.Fields(raw)
	textParts := make([]string, 0, len(fields))

	for _, f := range fields {
		lower := strings.ToLower(f)
		switch {
		case strings.HasPrefix(lower, "from:@"):
			name := strings.TrimPrefix(f[len("from:@"):], "")
			if name != "" {
				spec.From = append(spec.From, name)
			}
		case strings.HasPrefix(lower, "from:"):
			// `from:alice` (no @) is still accepted as a convenience.
			name := strings.TrimPrefix(f[len("from:"):], "")
			if name != "" {
				spec.From = append(spec.From, name)
			}
		case strings.HasPrefix(lower, "mentions:@"):
			name := f[len("mentions:@"):]
			if name != "" {
				spec.Mentions = append(spec.Mentions, name)
			}
		case strings.HasPrefix(lower, "in:#"):
			name := f[len("in:#"):]
			if name != "" {
				spec.InChannels = append(spec.InChannels, name)
			}
		case strings.HasPrefix(lower, "in:"):
			name := f[len("in:"):]
			if name != "" {
				spec.InChannels = append(spec.InChannels, name)
			}
		case strings.EqualFold(f, "has:link"):
			spec.HasLink = true
		case strings.EqualFold(f, "has:file"), strings.EqualFold(f, "has:attachment"):
			spec.HasFile = true
		default:
			textParts = append(textParts, f)
		}
	}

	spec.Text = strings.Join(textParts, " ")
	return spec
}
