package agentclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestJointBacklogInputAndUTCAnchor(t *testing.T) {
	for raw, want := range map[string]int{"": 0, "0": 0, "12": 12, " 720 ": 720, "8760": 8760} {
		got, err := parseJointBacklogHours(raw)
		if err != nil || got != want {
			t.Fatalf("valid backlog %q = %d, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"-1", "8761", "1.5", "day", strings.Repeat("9", 100)} {
		if _, err := parseJointBacklogHours(raw); err == nil {
			t.Fatalf("invalid backlog accepted: %q", raw)
		}
	}
	started := time.Date(2026, 10, 17, 20, 45, 0, 1, time.FixedZone("fixture-offset", 8*3600))
	anchor := jointHistoryAnchor(started, 12)
	if anchor.Location() != time.UTC || !anchor.Equal(time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("backlog anchor did not preserve UTC bucket boundaries")
	}
	if tail := started.UTC().Sub(anchor); tail != 12*time.Hour+45*time.Minute+time.Nanosecond {
		t.Fatal("data age and fractional setup tail were conflated")
	}
}

func TestJointBacklogPrivateFixtureLifecycle(t *testing.T) {
	parent := t.TempDir()
	if runtime.GOOS == "windows" {
		if _, _, err := newPrivateJointFixture(parent); err == nil {
			t.Fatal("accepted private fixture without POSIX permissions")
		}
		return
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(parent, "unrelated")
	if err := os.WriteFile(unrelated, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, finish, err := newPrivateJointFixture(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "joint.db"), []byte("synthetic fixture bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(root, "joint.db")); err != nil || string(data) != "synthetic fixture bytes" {
		t.Fatal("failed fixture was not preserved")
	}
	if err := recordJointFixturePlan(root, unrelated, 5, 12, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "fixture-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	var plan map[string]any
	decodeErr := json.Unmarshal(raw, &plan)
	digest, digestOK := plan["server_sha256"].(string)
	if decodeErr != nil || plan["backlog_hours"] != float64(12) || plan["nodes"] != float64(5) || plan["dense_year"] != true || !digestOK || len(digest) != 64 || strings.Contains(string(raw), parent) {
		t.Fatal("private fixture plan lost typed inputs or copied paths")
	}
	if err := recordJointFixturePlan(root, unrelated, 50, 24, true); err == nil {
		t.Fatal("private fixture plan was overwritten")
	}
	history := jointHistorySeed{Mode: "dense-year", BacklogHours: 12, Anchor: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), MetricRows: 123}
	if err := recordJointHistorySeed(root, history); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(root, "history-fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved jointHistorySeed
	if json.Unmarshal(raw, &saved) != nil || saved.BacklogHours != 12 || !saved.Anchor.Equal(history.Anchor) || saved.MetricRows != history.MetricRows {
		t.Fatal("actual seeded metadata was not preserved")
	}
	if err := recordJointHistorySeed(root, history); err == nil {
		t.Fatal("actual seeded metadata was overwritten")
	}
	if err := finish(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("successful fixture did not clean its owned child")
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "preserve" {
		t.Fatal("fixture cleanup touched a sibling")
	}

	root, finish, err = newPrivateJointFixture(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	if err := finish(false); err == nil {
		t.Fatal("cleanup accepted a replaced symlink")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("cleanup touched an outside target")
	}
}
