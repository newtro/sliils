package apps

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Block-Kit (M12-P2).
//
// Block-Kit is Slack's JSON schema for rich, interactive message
// content. We accept the same shape so existing Slack apps need no
// format changes to post into SliilS.
//
// Server responsibility is limited to VALIDATION: reject malformed
// shapes with a 400 so broken apps fail loud. The web client owns
// visual rendering.
//
// Supported block types at v1 (covers 95% of real Slack app traffic):
//   section   rich text + optional accessory
//   divider   visual rule
//   image     image URL + alt text
//   context   small meta text (e.g. "Posted by @alice 2m ago")
//   actions   a row of buttons / selects that emit actions back
//   header    big section title
//
// Unsupported types decode fine (we preserve the JSON verbatim) but
// produce a validation warning when strict mode is on.

// Block is a single top-level entry in message.body_blocks.
type Block struct {
	Type    string           `json:"type"`
	BlockID string           `json:"block_id,omitempty"`
	Text    *TextObject      `json:"text,omitempty"`
	Fields  []TextObject     `json:"fields,omitempty"`
	Elements []BlockElement  `json:"elements,omitempty"`
	ImageURL string          `json:"image_url,omitempty"`
	AltText  string          `json:"alt_text,omitempty"`
	Accessory *BlockElement  `json:"accessory,omitempty"`
}

type TextObject struct {
	Type  string `json:"type"`  // "plain_text" | "mrkdwn"
	Text  string `json:"text"`
	Emoji bool   `json:"emoji,omitempty"`
}

type BlockElement struct {
	Type     string         `json:"type"`    // "button" | "static_select" | ...
	Text     *TextObject    `json:"text,omitempty"`
	ActionID string         `json:"action_id,omitempty"`
	Value    string         `json:"value,omitempty"`
	URL      string         `json:"url,omitempty"`
	Style    string         `json:"style,omitempty"`
	Options  []OptionObject `json:"options,omitempty"`
}

type OptionObject struct {
	Text  TextObject `json:"text"`
	Value string     `json:"value"`
}

// ---- validation --------------------------------------------------------

// MaxBlocks caps the number of top-level blocks a single message can
// carry. Slack's own limit is 50; we mirror it.
const MaxBlocks = 50

// ValidateBlocksJSON decodes raw JSONB + checks structural rules.
// Returns nil for a nil/empty input so "body_blocks": [] works.
func ValidateBlocksJSON(raw []byte) ([]Block, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	// Empty array is legal.
	if string(raw) == "[]" || string(raw) == "null" {
		return nil, nil
	}
	var blocks []Block
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("blocks: invalid JSON: %w", err)
	}
	if len(blocks) > MaxBlocks {
		return nil, fmt.Errorf("blocks: at most %d allowed, got %d", MaxBlocks, len(blocks))
	}
	for i, b := range blocks {
		if err := validateBlock(b); err != nil {
			return nil, fmt.Errorf("blocks[%d]: %w", i, err)
		}
	}
	return blocks, nil
}

func validateBlock(b Block) error {
	switch b.Type {
	case "":
		return errors.New("missing type")
	case "section":
		if b.Text == nil && len(b.Fields) == 0 {
			return errors.New("section needs text or fields")
		}
		if b.Text != nil {
			if err := validateText(*b.Text); err != nil {
				return fmt.Errorf("text: %w", err)
			}
		}
	case "header":
		if b.Text == nil {
			return errors.New("header needs text")
		}
		if b.Text.Type != "plain_text" {
			return errors.New("header text must be plain_text")
		}
	case "divider", "context", "image", "actions":
		// Less strict checks at v1; preserve verbatim.
	default:
		// Unknown block type — accept but don't attempt interactive
		// validation. The client either renders or ignores.
	}
	return nil
}

func validateText(t TextObject) error {
	if t.Type != "plain_text" && t.Type != "mrkdwn" {
		return fmt.Errorf("text type %q must be plain_text or mrkdwn", t.Type)
	}
	if t.Text == "" {
		return errors.New("text cannot be empty")
	}
	if len(t.Text) > 3000 {
		return errors.New("text exceeds 3000 chars")
	}
	return nil
}

// EncodeBlocks serializes blocks back to JSONB. Normalises nil → "[]"
// so the DB column never stores NULL for empty states.
func EncodeBlocks(blocks []Block) []byte {
	if blocks == nil {
		return []byte("[]")
	}
	b, err := json.Marshal(blocks)
	if err != nil {
		return []byte("[]")
	}
	return b
}
