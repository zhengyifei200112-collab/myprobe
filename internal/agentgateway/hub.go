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

func (h *Hub) Publish(event Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			// A slow browser will receive a full snapshot on reconnect; ingestion must never block.
		}
	}
}
