package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func batchFixture(t *testing.T) (*Store, string, []string) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "batch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	user, err := s.CreateUser(ctx, "batch-admin", "test-only-hash")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, name := range []string{"one", "two"} {
		node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: name, Tags: []string{"old"}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, node.ID)
	}
	return s, user.ID, ids
}
func TestNodeBatchAtomicApplyReplayAndRestart(t *testing.T) {
	s, user, ids := batchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	hidden := true
	preview, err := s.PreviewNodeBatch(ctx, user, NodeBatchRequest{NodeIDs: ids, Hidden: &hidden, AddTags: []string{"new"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.ListPublicNodes(ctx, now)
	if len(before) != 2 {
		t.Fatal("preview changed visibility")
	}
	// Both retries serialize under the writer reservation and return one result.
	var wg sync.WaitGroup
	results := make([]NodeBatchResult, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = s.ApplyNodeBatch(ctx, user, preview.ID, "same-client-key-1234", now)
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Fatal("retry results differ")
	}
	if len(results[0].ChangedIDs) != 2 {
		t.Fatal(results[0])
	}
	visible, _ := s.ListPublicNodes(ctx, now)
	if len(visible) != 0 {
		t.Fatal("hidden nodes still public")
	}
	var count int
	s.db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action='batch_update'`).Scan(&count)
	if count != 1 {
		t.Fatalf("audit count %d", count)
	}
	path := s.path
	s.Close()
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, err := reopened.ApplyNodeBatch(ctx, user, preview.ID, "same-client-key-1234", now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replayed, results[0]) {
		t.Fatalf("restart replay: %v %+v", err, replayed)
	}
}
func TestNodeBatchConflictsAbortWholeBatchAndHeartbeatDoesNot(t *testing.T) {
	s, user, ids := batchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	hidden := true
	preview, err := s.PreviewNodeBatch(ctx, user, NodeBatchRequest{NodeIDs: ids, Hidden: &hidden}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE nodes SET name='changed' WHERE id=?`, ids[1]); err != nil {
		t.Fatal(err)
	}
	_, err = s.ApplyNodeBatch(ctx, user, preview.ID, "conflict-key-123456", now)
	var conflict *BatchConflict
	if !errors.As(err, &conflict) || !reflect.DeepEqual(conflict.NodeIDs, []string{ids[1]}) {
		t.Fatalf("conflict: %v", err)
	}
	visible, _ := s.ListPublicNodes(ctx, now)
	if len(visible) != 2 {
		t.Fatal("partial batch write")
	}
	preview, err = s.PreviewNodeBatch(ctx, user, NodeBatchRequest{NodeIDs: ids, Hidden: &hidden}, now)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Exec(`UPDATE nodes SET last_seen_at=? WHERE id=?`, formatTime(now), ids[0])
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "heartbeat-key-1234", now); err != nil {
		t.Fatal(err)
	}
}
func TestNodeBatchExpiryOwnershipValidationAndNoop(t *testing.T) {
	s, user, ids := batchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	hidden := false
	for _, req := range []NodeBatchRequest{{}, {NodeIDs: ids}, {NodeIDs: []string{ids[0], ids[0]}, Hidden: &hidden}, {NodeIDs: ids, AddTags: []string{"same"}, RemoveTags: []string{"same"}}, {NodeIDs: ids, Targets: &BatchTargets{Mode: "erase"}}} {
		if _, err := s.PreviewNodeBatch(ctx, user, req, now); !errors.Is(err, ErrBatchInvalid) {
			t.Fatalf("invalid request accepted: %+v %v", req, err)
		}
	}
	preview, err := s.PreviewNodeBatch(ctx, user, NodeBatchRequest{NodeIDs: ids, Hidden: &hidden}, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Nodes[0].Changed {
		t.Fatal("no-op marked changed")
	}
	if _, err = s.ApplyNodeBatch(ctx, "another-user", preview.ID, "owner-key-1234567", now); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "expired-key-12345", preview.ExpiresAt); !errors.Is(err, ErrBatchExpired) {
		t.Fatal(err)
	}
	result, err := s.ApplyNodeBatch(ctx, user, preview.ID, "noop-key-12345678", now)
	if err != nil || len(result.ChangedIDs) != 0 || len(result.UnchangedIDs) != 2 {
		t.Fatalf("no-op %v %+v", err, result)
	}
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "different-key-1234", now); !errors.Is(err, ErrBatchKey) {
		t.Fatal(err)
	}
	second, err := s.PreviewNodeBatch(ctx, user, NodeBatchRequest{NodeIDs: ids, Hidden: &hidden}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyNodeBatch(ctx, user, second.ID, "noop-key-12345678", now); !errors.Is(err, ErrBatchKey) {
		t.Fatal(err)
	}
}
func TestNodeBatchTargetsAndRollbackOnAuditFailure(t *testing.T) {
	s, user, ids := batchFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "test", Kind: "ping", Host: "example.com", IntervalSeconds: 60, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	request := NodeBatchRequest{NodeIDs: ids, Targets: &BatchTargets{Mode: "add", IDs: []string{target.ID}}}
	preview, err := s.PreviewNodeBatch(ctx, user, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_batch_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "audit-retry-12345", now); err == nil {
		t.Fatal("audit failure accepted")
	}
	assignments, _ := s.ListNodeTargets(ctx)
	if len(assignments) != 0 {
		t.Fatal("assignments escaped rollback")
	}
	s.db.Exec(`DROP TRIGGER reject_batch_audit`)
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "audit-retry-12345", now); err != nil {
		t.Fatal(err)
	}
	assignments, _ = s.ListNodeTargets(ctx)
	if len(assignments) != 2 {
		t.Fatal(assignments)
	}
	preview, err = s.PreviewNodeBatch(ctx, user, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Nodes[0].Changed {
		t.Fatal("duplicate add not no-op")
	}
	if err = s.UnassignTarget(ctx, ids[0], target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "assignment-change", now); err == nil {
		t.Fatal("assignment conflict ignored")
	}
	request.Targets = &BatchTargets{Mode: "replace", IDs: []string{}}
	preview, err = s.PreviewNodeBatch(ctx, user, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyNodeBatch(ctx, user, preview.ID, "replace-empty-123", now); err != nil {
		t.Fatal(err)
	}
	assignments, _ = s.ListNodeTargets(ctx)
	if len(assignments) != 0 {
		t.Fatal(assignments)
	}
}
