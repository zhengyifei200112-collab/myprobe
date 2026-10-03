package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIncidentQueriesPaginationAndPrivateFields(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90,"recovery_seconds":0}`)
	ctx := context.Background()
	now := time.Now().UTC()
	first := observe(t, s, rule, node, now, true, true)
	observe(t, s, rule, node, now.Add(time.Second), true, false)
	second := observe(t, s, rule, node, now.Add(2*time.Second), true, true)
	items, err := s.ListIncidents(ctx, IncidentFilter{Limit: 1})
	if err != nil || len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("first page: %+v %v", items, err)
	}
	items, err = s.ListIncidents(ctx, IncidentFilter{Limit: 1, Before: items[0].Seq})
	if err != nil || len(items) != 1 || items[0].ID != first.ID {
		t.Fatalf("second page: %+v %v", items, err)
	}
	items, err = s.ListIncidents(ctx, IncidentFilter{Limit: 100, State: "firing", NodeID: node.ID})
	if err != nil || len(items) != 1 || items[0].ID != second.ID {
		t.Fatalf("filtered: %+v %v", items, err)
	}
	raw, _ := json.Marshal(items)
	if strings.Contains(string(raw), "rule_snapshot") || strings.Contains(string(raw), "fingerprint") {
		t.Fatal("private incident metadata exposed")
	}
	jobs, err := s.ListIncidentDeliveries(ctx, second.ID, 0, 100)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	raw, _ = json.Marshal(jobs)
	for _, private := range []string{"payload", "lease_token", "idempotency_key"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("exposed %s", private)
		}
	}
	for _, filter := range []IncidentFilter{{Limit: 101}, {Limit: 1, Before: -1}, {Limit: 1, State: "unknown"}} {
		if _, err := s.ListIncidents(ctx, filter); err == nil {
			t.Fatalf("accepted %+v", filter)
		}
	}
}
