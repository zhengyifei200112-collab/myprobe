package httpcheck

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONAssertionExactMatching(t *testing.T) {
	for _, test := range []struct {
		body, want string
		match      bool
	}{
		{`{"value":9007199254740993}`, `9007199254740993`, true},
		{`{"value":9007199254740992}`, `9007199254740993`, false},
		{`{"value":1.00}`, `1e0`, true}, {`{"value":-0}`, `0.0`, true},
		{`{"value":1e999999999}`, `10e999999998`, true},
		{`{"value":null}`, `null`, true}, {`{}`, `null`, false},
		{`{"value":"1"}`, `1`, false}, {`{"value":true}`, `true`, true},
		{`{"value":[1]}`, `1`, false},
	} {
		a := Assertion{Kind: "json_equals", Path: []string{"value"}, Expected: json.RawMessage(test.want)}
		got, err := a.Matches([]byte(test.body))
		if err != nil || got != test.match {
			t.Fatalf("body=%s want=%s got=%v err=%v", test.body, test.want, got, err)
		}
	}
}

func TestAssertionBodyBoundaries(t *testing.T) {
	a := Assertion{Kind: "json_equals", Path: []string{"value"}, Expected: json.RawMessage(`true`)}
	for _, body := range []string{`{"value":false,"value":true}`, `{"value":true} {}`, `{"value":`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), strings.Repeat("x", 1<<20+1)} {
		if _, err := a.Matches([]byte(body)); err == nil {
			t.Fatal("invalid or ambiguous body accepted")
		}
	}
	a = Assertion{Kind: "text_contains", Text: "就绪"}
	if match, err := a.Matches([]byte("服务已就绪")); err != nil || !match {
		t.Fatal("UTF-8 text mismatch")
	}
	a = Assertion{Kind: "json_equals", Path: []string{"a.b"}, Expected: json.RawMessage(`true`)}
	if match, err := a.Matches([]byte(`{"a.b":true}`)); err != nil || !match {
		t.Fatal("literal field interpreted as expression")
	}
}
