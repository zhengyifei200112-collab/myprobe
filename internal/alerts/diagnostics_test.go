package alerts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestEngineDiagnosticsLifecycle(t *testing.T) {
	disabled := New(nil, "", nil, nil)
	disabled.Run(context.Background())
	if got := disabled.Diagnostics(); got.Status != "disabled" || got.Evaluation.State != "never_run" {
		t.Fatalf("disabled: %+v", got)
	}
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret := strings.Repeat("private-key", 4)
	s := New(db, secret, nil, nil)
	if s.Diagnostics().Status != "not_running" {
		t.Fatal("invented running engine")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for s.Diagnostics().Evaluation.CompletedRuns == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := s.Diagnostics(); got.Status != "running" || got.Evaluation.LastSuccessAt == nil {
		t.Fatalf("running: %+v", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("engine failed to stop")
	}
	if s.Diagnostics().Status != "not_running" {
		t.Fatal("stopped engine reported running")
	}
	db.Close()
	if s.Tick(context.Background(), time.Now()) == nil {
		t.Fatal("expected closed database failure")
	}
	got := s.Diagnostics()
	if got.Evaluation.State != "failed" || got.Evaluation.LastSuccessAt == nil {
		t.Fatalf("failure lost evidence: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "database is closed") {
		t.Fatal("private/raw error leaked")
	}
}
