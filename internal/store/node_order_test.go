package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"time"
)

func orderFixture(t *testing.T) (*Store, string, []NodeOrderEntry) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "order.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	user, err := s.CreateUser(ctx, "order-admin", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"same", "same", "last"} {
		node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: name, Tags: []string{"preserved"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.ExecContext(ctx, "UPDATE nodes SET sort_order=-4,hidden=1 WHERE id=?", node.ID); err != nil {
			t.Fatal(err)
		}
	}
	return s, user.ID, readOrder(t, s)
}
func readOrder(t *testing.T, s *Store) []NodeOrderEntry {
	t.Helper()
	nodes, err := s.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]NodeOrderEntry, 0, len(nodes))
	for _, node := range nodes {
		entries = append(entries, NodeOrderEntry{ID: node.ID, SortOrder: node.SortOrder})
	}
	return entries
}
func reverseOrder(entries []NodeOrderEntry) []string {
	ids := make([]string, len(entries))
	for index, entry := range entries {
		ids[len(entries)-1-index] = entry.ID
	}
	return ids
}
func TestNodeOrderAtomicRetryAndMetadata(t *testing.T) {
	s, user, before := orderFixture(t)
	ctx := context.Background()
	nodes, _ := s.ListNodes(ctx)
	// Equal names and values have a stable ID tie-break.
	if nodes[1].ID > nodes[2].ID {
		t.Fatal("unstable equal-name order")
	}
	request := NodeOrderRequest{Expected: before, NodeIDs: reverseOrder(before)}
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			changed, err := s.ReorderNodes(ctx, user, request)
			if err != nil {
				t.Error(err)
			}
			results <- changed
		}()
	}
	wg.Wait()
	close(results)
	changes := 0
	for changed := range results {
		if changed {
			changes++
		}
	}
	if changes != 1 {
		t.Fatalf("changed count=%d", changes)
	}
	after := readOrder(t, s)
	for index, entry := range after {
		if entry.ID != request.NodeIDs[index] || entry.SortOrder != index {
			t.Fatal("wrong saved order")
		}
	}
	current, _ := s.ListNodes(ctx)
	for _, node := range current {
		if !node.Hidden || !reflect.DeepEqual(node.Tags, []string{"preserved"}) || node.CollectionSeconds != nodes[0].CollectionSeconds {
			t.Fatal("unrelated metadata changed")
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM audit_log WHERE action='reorder'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit count=%d", count)
	}
	// Retry after reopening the database still performs no writes or audit.
	reopened, err := Open(ctx, s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if changed, err := reopened.ReorderNodes(ctx, user, request); err != nil || changed {
		t.Fatalf("replay: %v %v", changed, err)
	}
}
func TestNodeOrderRejectsStaleWholeList(t *testing.T) {
	for _, mutation := range []string{"create", "delete", "sort"} {
		t.Run(mutation, func(t *testing.T) {
			s, user, before := orderFixture(t)
			ctx := context.Background()
			switch mutation {
			case "create":
				if _, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "new"}); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := s.DeleteNode(ctx, before[0].ID); err != nil {
					t.Fatal(err)
				}
			case "sort":
				if _, err := s.db.ExecContext(ctx, "UPDATE nodes SET sort_order=99 WHERE id=?", before[0].ID); err != nil {
					t.Fatal(err)
				}
			}
			want := readOrder(t, s)
			if _, err := s.ReorderNodes(ctx, user, NodeOrderRequest{Expected: before, NodeIDs: reverseOrder(before)}); !errors.Is(err, ErrNodeOrderConflict) {
				t.Fatalf("err=%v", err)
			}
			if !reflect.DeepEqual(readOrder(t, s), want) {
				t.Fatal("conflict partially changed order")
			}
		})
	}
}
func TestNodeOrderNoopAndHeartbeat(t *testing.T) {
	s, user, before := orderFixture(t)
	ctx := context.Background()
	same := make([]string, len(before))
	for i, entry := range before {
		same[i] = entry.ID
	}
	if changed, err := s.ReorderNodes(ctx, user, NodeOrderRequest{Expected: before, NodeIDs: same}); err != nil || changed {
		t.Fatalf("noop %v %v", changed, err)
	}
	if !reflect.DeepEqual(readOrder(t, s), before) {
		t.Fatal("noop normalized existing integers")
	}
	if err := s.SaveReport(ctx, before[0].ID, protocol.Report{CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.ReorderNodes(ctx, user, NodeOrderRequest{Expected: before, NodeIDs: reverseOrder(before)}); err != nil || !changed {
		t.Fatalf("heartbeat invalidated order: %v %v", changed, err)
	}
}
func TestNodeOrderAuditFailureRollsBack(t *testing.T) {
	s, user, before := orderFixture(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_order_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(FAIL,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	request := NodeOrderRequest{Expected: before, NodeIDs: reverseOrder(before)}
	if _, err := s.ReorderNodes(ctx, user, request); err == nil {
		t.Fatal("expected failure")
	}
	if !reflect.DeepEqual(readOrder(t, s), before) {
		t.Fatal("failed audit left order writes")
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER fail_order_audit"); err != nil {
		t.Fatal(err)
	}
	if changed, err := s.ReorderNodes(ctx, user, request); err != nil || !changed {
		t.Fatalf("retry: %v %v", changed, err)
	}
}
func TestNodeOrderValidation(t *testing.T) {
	valid := []NodeOrderEntry{{ID: "a"}, {ID: "b"}}
	tests := []NodeOrderRequest{
		{}, {Expected: valid, NodeIDs: []string{"a"}},
		{Expected: valid, NodeIDs: []string{"a", "a"}},
		{Expected: valid, NodeIDs: []string{"a", "unknown"}},
		{Expected: []NodeOrderEntry{{ID: "a"}, {ID: "a"}}, NodeIDs: []string{"a", "a"}},
		{Expected: []NodeOrderEntry{{ID: " a"}}, NodeIDs: []string{" a"}},
		{Expected: make([]NodeOrderEntry, 1001), NodeIDs: make([]string, 1001)},
	}
	for _, request := range tests {
		if !errors.Is(request.validate(), ErrNodeOrderInvalid) {
			t.Fatalf("accepted %+v", request)
		}
	}
}
