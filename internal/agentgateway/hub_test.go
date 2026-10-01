package agentgateway

import "testing"

func TestRefreshReplacesFullQueue(t *testing.T) {
	h := NewHub()
	events, unsubscribe := h.Subscribe()
	defer unsubscribe()
	for i := 0; i < 100; i++ {
		h.Publish(Event{Type: "node_metrics"})
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
