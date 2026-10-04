package httpcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Path is a sequence of literal object keys, never executable JSONPath.
// Expected is a JSON scalar; a missing value differs from explicit null.
type Assertion struct {
	Kind     string          `json:"kind"`
	Text     string          `json:"text,omitempty"`
	Path     []string        `json:"path,omitempty"`
	Expected json.RawMessage `json:"expected,omitempty"`
}

func (a Assertion) Validate() error {
	switch a.Kind {
	case "text_contains":
		if len(a.Text) == 0 || len(a.Text) > 4096 || !utf8.ValidString(a.Text) || len(a.Path) != 0 || len(a.Expected) != 0 {
			return errors.New("text assertion requires bounded UTF-8 text only")
		}
	case "json_equals":
		if a.Text != "" || len(a.Path) == 0 || len(a.Path) > 16 || len(a.Expected) == 0 || len(a.Expected) > 4096 || !utf8.Valid(a.Expected) {
			return errors.New("JSON assertion requires a bounded field path and scalar value")
		}
		for _, key := range a.Path {
			if len(key) == 0 || len(key) > 128 || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) >= 0 {
				return errors.New("invalid JSON assertion field")
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(a.Expected))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return errors.New("invalid JSON assertion value")
		}
		switch value.(type) {
		case nil, string, bool, json.Number:
		default:
			return errors.New("JSON assertion value must be scalar")
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return errors.New("JSON assertion value must contain one scalar")
		}
	default:
		return errors.New("unsupported HTTP assertion")
	}
	return nil
}
