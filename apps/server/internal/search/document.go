package search

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MessageDoc is the Meilisearch document shape for a message. Field names are
// chosen to map cleanly to the filter DSL we issue in tenant tokens and on
// server-side queries.
//
// Design notes:
//   - id: we use the raw numeric message id stringified, so deletes can target
//     it precisely.
//   - channel_type: "public" | "private" | "dm" | "group_dm". Public channels
//     skip the member-id list at index-time; the filter
//     (channel_type = public OR channel_member_ids = U) makes them visible to
//     every workspace member without materializing membership.
//   - channel_member_ids: populated for non-public channels. Updated whenever
//     the message is re-indexed (body edit, etc.). Membership churn requires
//     a channel-wide reindex; that's out-of-band for M6 (accept the window
//     or issue a direct /reindex-channel call when membership changes).
//   - body_md: the raw markdown source. Meilisearch tokenizes this directly;
//     typo tolerance works per-word without any preprocessing.
//   - has_link / has_file: boolean flags used by the `has:link` / `has:file`
//     operators. Computed at index time so query-time cost stays constant.
//   - created_at_unix: numeric timestamp for sortability and `before:` /
//     `after:` operators (future). Meilisearch can't sort on ISO strings.
type MessageDoc struct {
	ID               string   `json:"id"`
	MessageID        int64    `json:"message_id"`
	WorkspaceID      int64    `json:"workspace_id"`
	ChannelID        int64    `json:"channel_id"`
	ChannelName      string   `json:"channel_name,omitempty"`
	ChannelType      string   `json:"channel_type"`
	ChannelMemberIDs []int64  `json:"channel_member_ids"`
	AuthorUserID     int64    `json:"author_user_id,omitempty"`
	ThreadRootID     int64    `json:"thread_root_id,omitempty"`
	BodyMD           string   `json:"body_md"`
	HasLink          bool     `json:"has_link"`
	HasFile          bool     `json:"has_file"`
	MentionUserIDs   []int64  `json:"mention_user_ids"`
	CreatedAtUnix    int64    `json:"created_at_unix"`
	CreatedAtISO     string   `json:"created_at_iso"`
	TextTokens       []string `json:"-"` // reserved for future use
}

// DocID is the stringified primary key Meilisearch uses. We keep it separate
// from MessageID so callers needing a strongly-typed numeric id don't have
// to parse.
func DocID(messageID int64) string {
	// Meilisearch doc ids are strings; keep the human-readable decimal form.
	return int64ToString(messageID)
}

// ---- body analysis -------------------------------------------------------

// linkRE matches http(s):// urls loosely — anything followed by non-space
// characters. Close enough for a boolean "has_link" flag.
var linkRE = regexp.MustCompile(`(?i)\bhttps?://\S+`)

// mentionRE matches the canonical <@N> mention token produced by the
// composer. Kept here (not imported from package server) to keep the search
// package standalone for tests.
var mentionRE = regexp.MustCompile(`<@(\d+)>`)

// AnalyzeBody scans a message body once and extracts the booleans + mention
// ids the index needs. Cheap enough to run on every index event.
func AnalyzeBody(bodyMD string) (hasLink bool, mentions []int64) {
	hasLink = linkRE.MatchString(bodyMD)
	matches := mentionRE.FindAllStringSubmatch(bodyMD, -1)
	if len(matches) == 0 {
		return hasLink, nil
	}
	seen := make(map[int64]struct{}, len(matches))
	out := make([]int64, 0, len(matches))
	for _, m := range matches {
		id, err := parseInt64(m[1])
		if err != nil {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return hasLink, out
}

// ---- plumbing ------------------------------------------------------------

// NormalizeChannelType trims the type the DB returns to the set Meili is
// configured to filter on. Defensive against future enum additions.
func NormalizeChannelType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "public", "private", "dm", "group_dm":
		return strings.ToLower(t)
	default:
		return "public"
	}
}

// UnixFromTime returns a seconds-precision timestamp. Meilisearch is happiest
// with integer numeric fields for sort + range filters.
func UnixFromTime(t time.Time) int64 {
	return t.UTC().Unix()
}

func int64ToString(n int64) string {
	return strconv.FormatInt(n, 10)
}

func parseInt64(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}
