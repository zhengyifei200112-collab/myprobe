package agentgateway

import "testing"

func TestRefreshReplacesFullQueue(t *testing.T) {
	h := NewHub()
	events, unsubscribe := h.Subscribe()
	defer unsubscribe()
	for i := 0; i < 100; i++ {
		h.Publish(h.Revision(), Event{Type: "node_metrics"})
	}
	h.PublishRefresh()
	if event := <-events; event.Type != "refresh" {
		t.Fatalf("queued event = %s", event.Type)
	}
	select {
	case event := <-events:
		t.Fatalf("stale event survived refresh: %s", event.Type)
	default:
	}
}

func TestRefreshInvalidatesInFlightPublicSnapshots(t *testing.T) {
	h := NewHub()
	events, unsubscribe := h.Subscribe()
	defer unsubscribe()
	before := h.Revision()
	h.PublishRefresh()
	h.Publish(before, Event{Type: "node_metrics"})
	if event := <-events; event.Type != "refresh" {
		t.Fatal("missing refresh")
	}
	select {
	case event := <-events:
		t.Fatalf("stale event after refresh: %+v", event)
	default:
	}
	h.Publish(h.Revision(), Event{Type: "node_metrics"})
	if event := <-events; event.Type != "node_metrics" {
		t.Fatal("current event rejected")
	}
}
