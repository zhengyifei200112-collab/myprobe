package agentclient

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

const networkResultCapacity = 32
const networkResultPayloadLimit = 4096
const networkResultRetryDelay = 5 * time.Second

type pendingNetworkResult struct {
	envelope       v2.Envelope
	expiresAt      time.Time
	lastAttempt    time.Time
	lastConnection *websocket.Conn
}

// One Run-owned worker retries bounded, immutable result envelopes until their
// application ACK or the original task's receive deadline. There is no disk
// queue, task re-execution, HTTP/v1 fallback or extension without negotiation.
type networkResultDelivery struct {
	once    sync.Once
	mu      sync.Mutex
	pending map[uint64]*pendingNetworkResult
	wake    chan struct{}
	stopped bool
}

func (d *networkResultDelivery) initialize() {
	d.once.Do(func() {
		d.pending = make(map[uint64]*pendingNetworkResult)
		d.wake = make(chan struct{}, 1)
	})
}

func (d *networkResultDelivery) signal() {
	d.initialize()
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

func (d *networkResultDelivery) start() {
	d.initialize()
	d.mu.Lock()
	d.stopped = false
	d.mu.Unlock()
}

func (c *Client) enqueueNetworkTaskResult(ctx context.Context, task check.Task, result check.Result) {
	now := time.Now().UTC()
	if ctx.Err() != nil || !now.Before(task.ExpiresAt) || result.ValidateAgainst(task, now) != nil {
		return
	}
	sequence := c.nextSequence(v2.TypeNetworkResult)
	envelope, err := v2.NewEnvelope(v2.TypeNetworkResult, sequence, result)
	if err != nil || len(envelope.Payload) > networkResultPayloadLimit {
		c.observeEnvelopeRejection(v2.TypeNetworkResult, sequence, "encoding")
		return
	}
	d := &c.networkResults
	d.initialize()
	d.mu.Lock()
	var expired []uint64
	for previous, item := range d.pending {
		if !now.Before(item.expiresAt) {
			delete(d.pending, previous)
			expired = append(expired, previous)
		}
	}
	class := ""
	if d.stopped || ctx.Err() != nil {
		class = "canceled"
	} else if len(d.pending) >= networkResultCapacity {
		class = "capacity"
	} else {
		d.pending[sequence] = &pendingNetworkResult{envelope: envelope, expiresAt: task.ExpiresAt}
	}
	d.mu.Unlock()
	for _, previous := range expired {
		c.rejectNetworkResult(previous, "expired")
	}
	if class != "" {
		c.rejectNetworkResult(sequence, class)
		return
	}
	d.signal()
}

func (c *Client) rejectNetworkResult(sequence uint64, class string) {
	c.observeEnvelopeRejection(v2.TypeNetworkResult, sequence, class)
	if c.logger != nil {
		c.logger.Warn("network result delivery ended without acknowledgement", "reason", class)
	}
}

// Return a due result without holding either lock during a socket write. A new
// negotiated connection can immediately retry the exact original sequence/body.
func (d *networkResultDelivery) next(now time.Time, connection *websocket.Conn) (*pendingNetworkResult, []uint64, time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var selected *pendingNetworkResult
	var expired []uint64
	wait := time.Hour
	for sequence, item := range d.pending {
		if !now.Before(item.expiresAt) {
			delete(d.pending, sequence)
			expired = append(expired, sequence)
			continue
		}
		wait = min(wait, item.expiresAt.Sub(now))
		if connection == nil {
			continue
		}
		due := item.lastConnection != connection || item.lastAttempt.IsZero() || !now.Before(item.lastAttempt.Add(networkResultRetryDelay))
		if due {
			if selected == nil || sequence < selected.envelope.Sequence {
				selected = item
			}
		} else {
			wait = min(wait, item.lastAttempt.Add(networkResultRetryDelay).Sub(now))
		}
	}
	return selected, expired, wait
}

func (c *Client) networkConnection() *websocket.Conn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	if c.connVersion != 2 || !c.connNetwork {
		return nil
	}
	return c.conn
}

func (c *Client) acknowledgeNetworkResult(connection *websocket.Conn, envelope v1.Envelope) {
	ack, err := v1.DecodePayload[v1.Acknowledgement](envelope)
	if err != nil || envelope.Version != v2.Version || envelope.Type != v1.TypeAcknowledged || ack.Sequence != envelope.Sequence {
		return
	}
	d := &c.networkResults
	d.initialize()
	// Connection lock precedes delivery lock everywhere both are needed. Reject
	// an old socket's ACK after a replacement or capability downgrade.
	c.connMu.RLock()
	if c.conn != connection || c.connVersion != 2 || !c.connNetwork {
		c.connMu.RUnlock()
		return
	}
	d.mu.Lock()
	if item := d.pending[ack.Sequence]; item != nil && item.lastConnection == connection {
		delete(d.pending, ack.Sequence)
	}
	d.mu.Unlock()
	c.connMu.RUnlock()
	d.signal()
}

func (c *Client) networkResultLoop(ctx context.Context) {
	d := &c.networkResults
	d.initialize()
	defer func() {
		d.mu.Lock()
		d.stopped = true
		pending := make([]uint64, 0, len(d.pending))
		for sequence := range d.pending {
			pending = append(pending, sequence)
			delete(d.pending, sequence)
		}
		d.mu.Unlock()
		for _, sequence := range pending {
			c.rejectNetworkResult(sequence, "canceled")
		}
	}()
	for ctx.Err() == nil {
		connection := c.networkConnection()
		item, expired, wait := d.next(time.Now(), connection)
		for _, sequence := range expired {
			c.rejectNetworkResult(sequence, "expired")
		}
		if item != nil {
			writeCtx, cancel := context.WithDeadline(ctx, minTime(time.Now().Add(10*time.Second), item.expiresAt))
			if !c.lockNetworkWriter(writeCtx) {
				cancel()
				continue
			}
			if c.networkConnection() != connection || writeCtx.Err() != nil {
				c.writeMu.Unlock()
				cancel()
				continue
			}
			d.mu.Lock()
			stillPending := d.pending[item.envelope.Sequence] == item && time.Now().Before(item.expiresAt)
			if stillPending {
				item.lastConnection, item.lastAttempt = connection, time.Now()
			}
			d.mu.Unlock()
			var err error
			if stillPending {
				err = c.writeV2Envelope(writeCtx, connection, item.envelope)
			}
			c.writeMu.Unlock()
			cancel()
			if err != nil {
				c.clearConnection(connection)
			}
			continue
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
		case <-d.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

func (c *Client) lockNetworkWriter(ctx context.Context) bool {
	if c.writeMu.TryLock() {
		return true
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if c.writeMu.TryLock() {
				return true
			}
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
