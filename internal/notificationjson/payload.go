package notificationjson

import (
	"bytes"
	"encoding/json"
	"errors"
)

var ErrTooLarge = errors.New("notification payload too large")

// Preserve existing bytes when they fit. Only remove optional HTML escaping
// when that encoding overhead would otherwise reject the same JSON data.
func MarshalBounded(value any, limit int) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) <= limit {
		return raw, nil
	}
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	raw = bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	if len(raw) > limit {
		return nil, ErrTooLarge
	}
	return raw, nil
}
