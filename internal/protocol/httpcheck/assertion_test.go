package httpcheck

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssertionContract(t *testing.T) {
	for _, value := range []string{`null`, `true`, `"健康"`, `9007199254740993`, `1.25e10`} {
		a := Assertion{Kind: "json_equals", Path: []string{"service", "status"}, Expected: json.RawMessage(value)}
		if err := a.Validate(); err != nil {
			t.Fatalf("valid scalar %s: %v", value, err)
		}
	}
	for _, value := range []string{"", `{}`, `[]`, `NaN`, `true false`, `"unterminated`, strings.Repeat("1", 4097)} {
		a := Assertion{Kind: "json_equals", Path: []string{"status"}, Expected: json.RawMessage(value)}
		if a.Validate() == nil {
			t.Errorf("accepted invalid scalar %q", value)
		}
	}
	for _, a := range []Assertion{
		{Kind: "text_contains"}, {Kind: "text_contains", Text: strings.Repeat("x", 4097)},
		{Kind: "text_contains", Text: "ok", Expected: json.RawMessage(`true`)},
		{Kind: "json_equals", Path: []string{""}, Expected: json.RawMessage(`null`)},
		{Kind: "json_equals", Path: []string{"bad\nkey"}, Expected: json.RawMessage(`null`)},
		{Kind: "json_equals", Path: make([]string, 17), Expected: json.RawMessage(`true`)},
		{Kind: "script", Text: "true"},
	} {
		if a.Validate() == nil {
			t.Errorf("accepted invalid assertion: %+v", a)
		}
	}
	a := Assertion{Kind: "text_contains", Text: "ready"}
	s := Spec{URL: "https://example.com", Method: "HEAD", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024, Assertion: &a}
	if s.Validate() == nil {
		t.Fatal("HEAD body assertion accepted")
	}
	s.Method = "GET"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}
