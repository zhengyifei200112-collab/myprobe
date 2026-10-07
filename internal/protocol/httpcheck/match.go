package httpcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"strings"
	"unicode/utf8"
)

// Matches evaluates a bounded decoded body, without including its contents in
// errors. The executor must enforce its possibly smaller configured body limit.
func (a Assertion) Matches(body []byte) (bool, error) {
	if err := a.Validate(); err != nil {
		return false, err
	}
	if len(body) > 1<<20 || !utf8.Valid(body) {
		return false, errors.New("invalid assertion body")
	}
	if a.Kind == "text_contains" {
		return bytes.Contains(body, []byte(a.Text)), nil
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	value, err := readJSONValue(d, 0)
	if err != nil {
		return false, errors.New("invalid JSON assertion body")
	}
	if _, err := d.Token(); err != io.EOF {
		return false, errors.New("invalid JSON assertion body")
	}
	for _, key := range a.Path {
		object, ok := value.(map[string]any)
		if !ok {
			return false, nil
		}
		value, ok = object[key]
		if !ok {
			return false, nil
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(a.Expected))
	decoder.UseNumber()
	var expected any
	if err := decoder.Decode(&expected); err != nil {
		return false, errors.New("invalid assertion value")
	}
	switch want := expected.(type) {
	case nil:
		return value == nil, nil
	case string:
		got, ok := value.(string)
		return ok && got == want, nil
	case bool:
		got, ok := value.(bool)
		return ok && got == want, nil
	case json.Number:
		got, ok := value.(json.Number)
		if !ok {
			return false, nil
		}
		return canonicalNumber(string(got)) == canonicalNumber(string(want)), nil
	}
	return false, nil
}

// Reject duplicate keys and excessive nesting rather than choosing an ambiguous
// interpretation of a service response. Numbers are never converted to float64.
func readJSONValue(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("JSON nesting limit")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid object key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate object key")
			}
			value, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := d.Token()
		return object, err
	case json.Delim('['):
		for d.More() {
			if _, err := readJSONValue(d, depth+1); err != nil {
				return nil, err
			}
		}
		_, err := d.Token()
		return []any{}, err // arrays cannot be traversed or compared
	default:
		if number, ok := token.(json.Number); ok && len(number) > 4096 {
			return nil, errors.New("JSON number limit")
		}
		return token, nil
	}
}

// Canonical decimal coefficient/exponent comparison avoids both binary rounding
// and expansion of enormous exponents. Inputs have already passed JSON parsing.
func canonicalNumber(raw string) string {
	negative := strings.HasPrefix(raw, "-")
	raw = strings.TrimPrefix(raw, "-")
	exponent := new(big.Int)
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		exponent.SetString(raw[index+1:], 10)
		raw = raw[:index]
	}
	if index := strings.IndexByte(raw, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(raw)-index-1)))
		raw = raw[:index] + raw[index+1:]
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return "0"
	}
	trimmed := strings.TrimRight(raw, "0")
	exponent.Add(exponent, big.NewInt(int64(len(raw)-len(trimmed))))
	if negative {
		trimmed = "-" + trimmed
	}
	return trimmed + "e" + exponent.String()
}
