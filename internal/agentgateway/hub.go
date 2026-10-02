package agentgateway

import (
	"sync"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type Event struct {
	Type string           `json:"type"`
	Node store.PublicNode `json:"node"`
}

type Hub struct {
	mu          sync.RWMutex
	subscribers map[chan Event]struct{}
	revision    uint64
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan Event]struct{})}
}

func (h *Hub) Subscribe() (<-chan Event, func()) {
	channel := make(chan Event, 16)
	h.mu.Lock()
	h.subscribers[channel] = struct{}{}
	h.mu.Unlock()
	return channel, func() {
		h.mu.Lock()
		if _, ok := h.subscribers[channel]; ok {
			delete(h.subscribers, channel)
			close(channel)
		}
		h.mu.Unlock()
	}
}

// Revision must be captured before reading a public node snapshot.
func (h *Hub) Revision() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.revision
}

func (h *Hub) Publish(revision uint64, event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	// A visibility refresh invalidates reads that started before it, including
	// publishers still in flight when the queued events were drained.
	if revision != h.revision {
		return
	}
	for subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			// A slow browser will receive a full snapshot on reconnect; ingestion must never block.
		}
	}
}

// PublishRefresh replaces queued metrics with a signal to read a current public
// snapshot. Unlike best-effort samples, visibility changes must not be dropped.
func (h *Hub) PublishRefresh() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revision++
	for subscriber := range h.subscribers {
	drain:
		for {
			select {
			case <-subscriber:
			default:
				break drain
			}
		}
		subscriber <- Event{Type: "refresh"}
	}
}
