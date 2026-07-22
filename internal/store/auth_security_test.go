package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptchaChallengeIsBoundAndConsumedOnce(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "captcha.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	if err := database.CreateCaptchaChallenge(ctx, "challenge", "admin", "192.0.2.1", "answer-hash", now.Add(time.Minute), now); err != nil {
		t.Fatal(err)
	}
	if valid, err := database.ConsumeCaptchaChallenge(ctx, "challenge", "admin", "192.0.2.2", "answer-hash", now); err != nil || valid {
		t.Fatalf("wrong source = %v, %v", valid, err)
	}
	// A source mismatch must not consume a challenge belonging to another source.
	if valid, err := database.ConsumeCaptchaChallenge(ctx, "challenge", "admin", "192.0.2.1", "answer-hash", now); err != nil || !valid {
		t.Fatalf("valid challenge = %v, %v", valid, err)
	}
	if valid, err := database.ConsumeCaptchaChallenge(ctx, "challenge", "admin", "192.0.2.1", "answer-hash", now); err != nil || valid {
		t.Fatalf("reused challenge = %v, %v", valid, err)
	}
}

func TestAuditCursorPagination(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	user, err := database.CreateUser(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := database.LogAudit(ctx, user.ID, "update", "node", string(rune('a'+i)), "192.0.2.1", map[string]int{"index": i}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := database.ListAudit(ctx, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ID <= first[1].ID {
		t.Fatalf("first page = %#v", first)
	}
	second, err := database.ListAudit(ctx, 2, first[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].ID >= first[1].ID {
		t.Fatalf("second page = %#v", second)
	}
}

func TestLoginGuardHandlesRFC3339FractionBoundary(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "guard.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Date(2026, 7, 22, 1, 2, 3, 500_000_000, time.UTC)
	for i := 0; i < 5; i++ {
		if err := database.RecordLoginFailure(ctx, "admin", "192.0.2.1", now.Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := database.LoginGuard(ctx, "admin", "192.0.2.1", now)
	if err != nil {
		t.Fatal(err)
	}
	if guard.BlockedUntil == nil || !guard.CaptchaRequired {
		t.Fatalf("guard = %#v", guard)
	}
}
