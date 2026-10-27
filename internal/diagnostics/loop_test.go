package diagnostics

import (
	"testing"
	"time"
)

func TestLoopRecordsActualEntryExitAndCopiesEvidence(t *testing.T) {
	var loop Loop
	if loop.Snapshot().Status != "never_started" {
		t.Fatal("invented running loop")
	}
	stop := loop.Begin()
	loop.Schedule(time.Second)
	first := loop.Snapshot()
	if first.Status != "running" || first.ActiveLoops != 1 || first.NextRunAt == nil {
		t.Fatal("missing running evidence")
	}
	*first.NextRunAt = time.Time{}
	if loop.Snapshot().NextRunAt.IsZero() {
		t.Fatal("snapshot aliases mutable state")
	}
	stop()
	stop()
	last := loop.Snapshot()
	if last.Status != "not_running" || last.ActiveLoops != 0 || last.NextRunAt != nil || last.LastStoppedAt == nil {
		t.Fatal("stopped loop still running")
	}
}
