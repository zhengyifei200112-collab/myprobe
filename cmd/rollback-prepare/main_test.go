package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCommandRejectsArgumentsWithoutSuccessReceipt(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
	}{
		{"missing required inputs", nil, 1},
		{"unknown option", []string{"--not-a-supported-option"}, 2},
		{"unexpected positional value", []string{"operator-private-value"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("exit code %d, want %d", code, test.code)
			}
			if stdout.Len() != 0 || stderr.Len() == 0 || strings.Contains(stderr.String(), "operator-private-value") {
				t.Fatal("failure emitted a success receipt, omitted its error or exposed positional input")
			}
		})
	}
}

func TestCommandHelpDoesNotPrepare(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatal(code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "expected-sha256") {
		t.Fatal("help should document inputs without a success receipt")
	}
}
