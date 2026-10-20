package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestNodeBatchPendingQuotaReclaimsExpiredPreviews(t *testing.T) {
	for _, afterExpiry := range []time.Duration{0, time.Nanosecond, 500 * time.Millisecond} {
		t.Run(afterExpiry.String(), func(t *testing.T) {
			s, user, nodes := batchFixture(t)
			ctx := context.Background()
			now := time.Date(2026, 1, 2, 12, 0, 0, 500000000, time.UTC)
			request := NodeBatchRequest{NodeIDs: nodes, AddTags: []string{"quota"}}
			var first NodeBatchPreview
			for i := 0; i < 100; i++ {
				p, err := s.PreviewNodeBatch(ctx, user, request, now)
				if err != nil {
					t.Fatalf("preview %d: %v", i, err)
				}
				if i == 0 {
					first = p
				}
			}
			if _, err := s.PreviewNodeBatch(ctx, user, request, first.ExpiresAt.Add(-time.Nanosecond)); !errors.Is(err, ErrBatchInvalid) {
				t.Fatalf("101st unexpired preview: %v", err)
			}
			other, err := s.CreateUser(ctx, "other-batch-admin", "test-only-hash")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.PreviewNodeBatch(ctx, other.ID, request, now.Add(time.Minute)); err != nil {
				t.Fatalf("quota must be per user: %v", err)
			}
			if _, err := s.PreviewNodeBatch(ctx, user, request, first.ExpiresAt.Add(afterExpiry)); err != nil {
				t.Fatalf("expired previews still consume quota: %v", err)
			}
			var count int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_batch_previews`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("new preview and other user's unexpired preview: count=%d err=%v", count, err)
			}
		})
	}
}

func TestNodeBatchCleanupKeepsUnexpiredFractionalPreview(t *testing.T) {
	s, user, nodes := batchFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 12, 0, 0, 500000000, time.UTC)
	request := NodeBatchRequest{NodeIDs: nodes, AddTags: []string{"not-expired"}}
	preview, err := s.PreviewNodeBatch(ctx, user, request, now)
	if err != nil {
		t.Fatal(err)
	}
	beforeExpiry := preview.ExpiresAt.Add(-500 * time.Millisecond)
	if _, err := s.PreviewNodeBatch(ctx, user, request, beforeExpiry); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyNodeBatch(ctx, user, preview.ID, "unexpired-request-1234", beforeExpiry); err != nil {
		t.Fatalf("cleanup removed a preview before its real expiry: %v", err)
	}
}

func TestNodeBatchAppliedResultRetentionBoundaries(t *testing.T) {
	s, user, nodes := batchFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 12, 0, 0, 500000000, time.UTC)
	request := NodeBatchRequest{NodeIDs: nodes, AddTags: []string{"retained"}}
	preview, err := s.PreviewNodeBatch(ctx, user, request, now)
	if err != nil {
		t.Fatal(err)
	}
	const key = "retained-client-key-1234"
	result, err := s.ApplyNodeBatch(ctx, user, preview.ID, key, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, age := range []time.Duration{30*24*time.Hour - 500*time.Millisecond, 30 * 24 * time.Hour} {
		if _, err := s.PreviewNodeBatch(ctx, user, request, now.Add(age)); err != nil {
			t.Fatal(err)
		}
		replay, err := s.ApplyNodeBatch(ctx, user, preview.ID, key, now.Add(age))
		if err != nil || !reflect.DeepEqual(replay, result) {
			t.Fatalf("retained replay at boundary %d: %+v %v", i, replay, err)
		}
		if _, err := s.ApplyNodeBatch(ctx, user, preview.ID, fmt.Sprintf("changed-key-1234-%d", i), now.Add(age)); !errors.Is(err, ErrBatchKey) {
			t.Fatalf("retained key conflict: %v", err)
		}
	}
	if _, err := s.PreviewNodeBatch(ctx, user, request, now.Add(30*24*time.Hour+time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyNodeBatch(ctx, user, preview.ID, key, now.Add(30*24*time.Hour+time.Nanosecond)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("older-than-30-day result survived cleanup: %v", err)
	}
}

func TestNodeBatchQuotaAndReplaySurviveSnapshot(t *testing.T) {
	s, user, nodes := batchFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 12, 0, 0, 123456789, time.UTC)
	request := NodeBatchRequest{NodeIDs: nodes, AddTags: []string{"snapshot"}}
	preview, err := s.PreviewNodeBatch(ctx, user, request, now)
	if err != nil {
		t.Fatal(err)
	}
	const key = "snapshot-client-key-1234"
	result, err := s.ApplyNodeBatch(ctx, user, preview.ID, key, now)
	if err != nil {
		t.Fatal(err)
	}
	request.AddTags = []string{"pending-after-snapshot"}
	var pending NodeBatchPreview
	for i := 0; i < 100; i++ {
		pending, err = s.PreviewNodeBatch(ctx, user, request, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.PreviewNodeBatch(ctx, user, request, now.Add(2*time.Minute)); !errors.Is(err, ErrBatchInvalid) {
		t.Fatalf("restore lost the pending-preview quota: %v", err)
	}
	replay, err := restored.ApplyNodeBatch(ctx, user, preview.ID, key, now.Add(2*time.Minute))
	if err != nil || !reflect.DeepEqual(replay, result) {
		t.Fatalf("restore changed the frozen result: %+v %v", replay, err)
	}
	if _, err := restored.PreviewNodeBatch(ctx, user, request, pending.ExpiresAt); err != nil {
		t.Fatalf("restored pending quota not released at expiry: %v", err)
	}
	replay, err = restored.ApplyNodeBatch(ctx, user, preview.ID, key, pending.ExpiresAt)
	if err != nil || !reflect.DeepEqual(replay, result) {
		t.Fatalf("pending cleanup changed an applied result: %+v %v", replay, err)
	}
	var previews, audits int
	if err := restored.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_batch_previews`).Scan(&previews); err != nil {
		t.Fatal(err)
	}
	if err := restored.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_log WHERE action='batch_update'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if previews != 2 || audits != 1 {
		t.Fatalf("restore/cleanup duplicated work: previews=%d audits=%d", previews, audits)
	}
}
