package search

import (
	"reflect"
	"testing"
)

func TestParseQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want QuerySpec
	}{
		{
			name: "empty",
			in:   "",
			want: QuerySpec{},
		},
		{
			name: "plain text only",
			in:   "ship the release",
			want: QuerySpec{Text: "ship the release"},
		},
		{
			name: "from at-prefixed",
			in:   "from:@alice hello",
			want: QuerySpec{Text: "hello", From: []string{"alice"}},
		},
		{
			name: "from bare name",
			in:   "from:bob roadmap",
			want: QuerySpec{Text: "roadmap", From: []string{"bob"}},
		},
		{
			name: "in channel with hash",
			in:   "in:#design timing",
			want: QuerySpec{Text: "timing", InChannels: []string{"design"}},
		},
		{
			name: "in channel bare",
			in:   "in:random lunch",
			want: QuerySpec{Text: "lunch", InChannels: []string{"random"}},
		},
		{
			name: "has link / file",
			in:   "has:link has:file diagram",
			want: QuerySpec{Text: "diagram", HasLink: true, HasFile: true},
		},
		{
			name: "has attachment alias",
			in:   "has:attachment notes",
			want: QuerySpec{Text: "notes", HasFile: true},
		},
		{
			name: "mentions operator",
			in:   "mentions:@carol notes",
			want: QuerySpec{Text: "notes", Mentions: []string{"carol"}},
		},
		{
			name: "case insensitive operators, preserved text",
			in:   "FROM:@alice In:#DESIGN Has:Link Buffer Overflow",
			want: QuerySpec{
				Text:       "Buffer Overflow",
				From:       []string{"alice"},
				InChannels: []string{"DESIGN"},
				HasLink:    true,
			},
		},
		{
			name: "unknown operator stays in text",
			in:   "since:yesterday urgent",
			want: QuerySpec{Text: "since:yesterday urgent"},
		},
		{
			name: "kickoff demo example",
			in:   "from:@alice has:link in:#design",
			want: QuerySpec{
				From:       []string{"alice"},
				InChannels: []string{"design"},
				HasLink:    true,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseQuery(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseQuery(%q)\n got = %#v\nwant = %#v", tt.in, got, tt.want)
			}
		})
	}
}
