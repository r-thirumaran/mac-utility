package httpserve

import (
	"bytes"
	"encoding/json"
)

// marshal encodes v compactly without HTML escaping so that UI strings such
// as "<b>" arrive unchanged.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
