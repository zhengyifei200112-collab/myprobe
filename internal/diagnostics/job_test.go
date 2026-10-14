package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJobTracksOverlapWithoutLosingLastResult(t *testing.T) {
	var job Job
	if initial := job.Snapshot(); initial.State != "never_run" || initial.LastStartedAt != nil || initial.LastDurationSeconds != nil {
		t.Fatalf("new job invented evidence: %+v", initial)
	}
	first, second := job.Begin(), job.Begin()
	first(nil)
	first(errors.New("duplicate completion"))
	active := job.Snapshot()
	if active.State != "running" || active.ActiveRuns != 1 || active.CompletedRuns != 1 || active.LastResult != "success" || active.LastSuccessAt == nil {
		t.Fatalf("overlapping runs: %+v", active)
	}
	second(errors.New("secret-password /private/path 198.51.100.123"))
	failed := job.Snapshot()
	if failed.State != "failed" || failed.CompletedRuns != 2 || failed.FailedRuns != 1 || failed.LastSuccessAt == nil || failed.LastDurationSeconds == nil || *failed.LastDurationSeconds < 0 {
		t.Fatalf("failed run: %+v", failed)
	}
	encoded, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"secret-password", "/private/path", "198.51.100.123"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("raw error leaked: %s", encoded)
		}
	}
	*failed.LastStartedAt = time.Time{}
	*failed.LastDurationSeconds = -1
	if fresh := job.Snapshot(); fresh.LastStartedAt.IsZero() || *fresh.LastDurationSeconds < 0 {
		t.Fatal("snapshot mutated stored evidence")
	}
}

func TestJobConcurrentCompletionAndCancellation(t *testing.T) {
	var job Job
	var wait sync.WaitGroup
	for i := 0; i < 100; i++ {
		finish := job.Begin()
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			if i%2 == 0 {
				finish(context.Canceled)
			} else {
				finish(context.DeadlineExceeded)
			}
			finish(nil)
			_ = job.Snapshot()
		}(i)
	}
	wait.Wait()
	result := job.Snapshot()
	if result.ActiveRuns != 0 || result.CompletedRuns != 100 || result.CancelledRuns != 50 || result.FailedRuns != 50 || result.LastSuccessAt != nil {
		t.Fatalf("concurrent evidence: %+v", result)
	}
}
