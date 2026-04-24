package realtime

import "encoding/json"

// marshalJSON is a tiny helper so presence/typing can emit payloads
// without dragging the encoding/json import into their files.
func marshalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}
