package agentclient

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func networkDeliveryFixture(expires time.Time) (check.Task, check.Result) {
	task := check.Task{Task: v1.Task{ID: "delivery-task", TargetID: "delivery-target", Kind: v1.TaskKindTCPing, Host: "127.0.0.1", Port: 80, TimeoutMS: 1000, ExpiresAt: expires}, AddressFamily: check.FamilyAuto}
	rtt := 1.0
	result := check.Result{TaskID: task.ID, TargetID: task.TargetID, Kind: task.Kind, Outcome: check.Success, AddressFamily: check.FamilyIPv4, RTTMS: &rtt, CompletedAt: time.Now().UTC()}
	return task, result
}

func networkPendingCount(client *Client) int {
	client.networkResults.initialize()
	client.networkResults.mu.Lock()
	defer client.networkResults.mu.Unlock()
	return len(client.networkResults.pending)
}

func TestNetworkDeliveryCapacityKeepsAnExplicitRejectedSequence(t *testing.T) {
	var events []clientEnvelopeEvent
	client := &Client{observeEnvelope: func(event clientEnvelopeEvent) { events = append(events, event) }}
	task, result := networkDeliveryFixture(time.Now().Add(time.Minute))
	for range networkResultCapacity + 1 {
		client.enqueueNetworkTaskResult(context.Background(), task, result)
	}
	if networkPendingCount(client) != networkResultCapacity || client.sequence.Load() != networkResultCapacity+1 {
		t.Fatal("overflow must bound memory and preserve the generated/rejected attempt")
	}
	last := events[len(events)-1]
	if last.Phase != "local_rejected" || last.ErrorClass != "capacity" || last.Sequence != networkResultCapacity+1 {
		t.Fatal("capacity loss was hidden instead of explicitly observed", last)
	}
	for _, item := range client.networkResults.pending {
		if len(item.envelope.Payload) > networkResultPayloadLimit {
			t.Fatal("retained result payload exceeded its per-entry bound")
		}
	}
}

func TestNetworkDeliveryExpiresAgainstOriginalTaskAndRetriesSameEnvelope(t *testing.T) {
	client := &Client{}
	now := time.Now()
	task, result := networkDeliveryFixture(now.Add(20 * time.Second))
	client.enqueueNetworkTaskResult(context.Background(), task, result)
	delivery := &client.networkResults
	// Socket identities suffice for this scheduling/fencing unit. Real writes,
	// reconnects, application ACKs and SQL dedupe have separate end-to-end tests.
	first, second := new(websocket.Conn), new(websocket.Conn)
	item, expired, _ := delivery.next(now, first)
	if item == nil || len(expired) != 0 {
		t.Fatal("new result was not immediately eligible")
	}
	originalBody, originalSequence := string(item.envelope.Payload), item.envelope.Sequence
	delivery.mu.Lock()
	item.lastConnection, item.lastAttempt = first, now
	delivery.mu.Unlock()
	if retry, _, _ := delivery.next(now.Add(time.Second), first); retry != nil {
		t.Fatal("unacknowledged result retried too early on the same connection")
	}
	for _, observation := range []struct {
		at   time.Time
		conn *websocket.Conn
	}{{now.Add(networkResultRetryDelay), first}, {now.Add(time.Second), second}} {
		retry, _, _ := delivery.next(observation.at, observation.conn)
		if retry != item || retry.envelope.Sequence != originalSequence || string(retry.envelope.Payload) != originalBody {
			t.Fatal("retry must preserve the exact original sequence and payload")
		}
	}
	if retry, expired, _ := delivery.next(task.ExpiresAt, second); retry != nil || len(expired) != 1 || networkPendingCount(client) != 0 {
		t.Fatal("result outlived the original task receive deadline")
	}
}

func TestNetworkDeliveryACKRequiresAnAttemptOnCurrentNegotiatedSocket(t *testing.T) {
	for _, test := range []struct {
		name      string
		attempted bool
		current   bool
		allowed   bool
		version   int
		wire      int
		bodySeq   uint64
		wantClear bool
	}{
		{"not_written", false, true, true, 2, 2, 1, false},
		{"stale_connection", true, false, true, 2, 2, 1, false},
		{"capability_removed", true, true, false, 2, 2, 1, false},
		{"legacy_connection", true, true, true, 1, 1, 1, false},
		{"wrong_wire_version", true, true, true, 2, 1, 1, false},
		{"wrong_body_sequence", true, true, true, 2, 2, 2, false},
		{"current_matching_ack", true, true, true, 2, 2, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &Client{}
			task, result := networkDeliveryFixture(time.Now().Add(time.Minute))
			client.enqueueNetworkTaskResult(context.Background(), task, result)
			sender, other := new(websocket.Conn), new(websocket.Conn)
			client.conn, client.connVersion, client.connNetwork = sender, test.version, test.allowed
			if !test.current {
				client.conn = other
			}
			if test.attempted {
				client.networkResults.pending[1].lastConnection = sender
			}
			ack, err := v1.NewEnvelope(v1.TypeAcknowledged, 1, v1.Acknowledgement{Sequence: test.bodySeq})
			if err != nil {
				t.Fatal(err)
			}
			ack.Version = test.wire
			client.acknowledgeNetworkResult(sender, ack)
			if (networkPendingCount(client) == 0) != test.wantClear {
				t.Fatal("incorrect ACK cleared or retained the result")
			}
		})
	}
}

func TestNetworkDeliveryShutdownClearsPendingAndRejectsLateWorkers(t *testing.T) {
	client := &Client{}
	task, result := networkDeliveryFixture(time.Now().Add(time.Minute))
	client.enqueueNetworkTaskResult(context.Background(), task, result)
	client.conn, client.connVersion, client.connNetwork = new(websocket.Conn), 2, true
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); client.networkResultLoop(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("delivery worker ignored cancellation while the writer remained held")
	}
	if networkPendingCount(client) != 0 || !client.networkResults.stopped {
		t.Fatal("delivery work was retained after the owning Run stopped")
	}
	client.enqueueNetworkTaskResult(context.Background(), task, result)
	if networkPendingCount(client) != 0 {
		t.Fatal("a late task worker reintroduced work after shutdown")
	}
}

func TestNetworkDeliveryWriterAcquisitionIsCancellable(t *testing.T) {
	client := &Client{}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finished := make(chan bool, 1)
	go func() { finished <- client.lockNetworkWriter(ctx) }()
	select {
	case acquired := <-finished:
		if acquired {
			client.writeMu.Unlock()
			t.Fatal("canceled waiter acquired the held writer")
		}
	case <-time.After(time.Second):
		t.Fatal("writer acquisition ignored cancellation")
	}
}

func TestNetworkDeliveryTaskDeadlineStopsHeldWriterWithoutSocketWrite(t *testing.T) {
	expired := make(chan clientEnvelopeEvent, 1)
	client := &Client{observeEnvelope: func(event clientEnvelopeEvent) {
		if event.Phase == "local_rejected" {
			expired <- event
		}
	}}
	task, result := networkDeliveryFixture(time.Now().Add(150 * time.Millisecond))
	client.enqueueNetworkTaskResult(context.Background(), task, result)
	client.conn, client.connVersion, client.connNetwork = new(websocket.Conn), 2, true
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); client.networkResultLoop(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case event := <-expired:
		if event.Sequence != 1 || event.ErrorClass != "expired" {
			t.Fatal("task deadline was changed or hidden", event)
		}
	case <-time.After(time.Second):
		t.Fatal("task deadline failed to bound the held-writer wait")
	}
	if networkPendingCount(client) != 0 {
		t.Fatal("expired result remained in memory")
	}
}

func TestNetworkDeliveryDoesNotAllocateForInvalidCancelledOrExpiredTask(t *testing.T) {
	for _, variant := range []string{"expired", "cancelled", "wrong_task", "wrong_family"} {
		t.Run(variant, func(t *testing.T) {
			client := &Client{}
			task, result := networkDeliveryFixture(time.Now().Add(time.Minute))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch variant {
			case "expired":
				task.ExpiresAt = time.Now().Add(-time.Second)
			case "cancelled":
				cancel()
			case "wrong_task":
				result.TaskID = "another-task"
			case "wrong_family":
				task.AddressFamily, result.AddressFamily = check.FamilyIPv4, check.FamilyIPv6
			}
			client.enqueueNetworkTaskResult(ctx, task, result)
			if client.sequence.Load() != 0 || networkPendingCount(client) != 0 {
				t.Fatal("inadmissible result allocated a generated envelope")
			}
		})
	}
}

func TestNetworkDeliveryAdmissionIsBoundedAcrossConcurrentExecutors(t *testing.T) {
	client := &Client{}
	task, result := networkDeliveryFixture(time.Now().Add(time.Minute))
	var workers sync.WaitGroup
	for range 128 {
		workers.Add(1)
		go func() { defer workers.Done(); client.enqueueNetworkTaskResult(context.Background(), task, result) }()
	}
	workers.Wait()
	if networkPendingCount(client) != networkResultCapacity || client.sequence.Load() != 128 {
		t.Fatal("concurrent admission escaped the queue bound or hid generated attempts")
	}
}

func TestNetworkDeliveryAdmissionReclaimsExpiredEntries(t *testing.T) {
	client := &Client{}
	task, result := networkDeliveryFixture(time.Now().Add(time.Minute))
	for range networkResultCapacity {
		client.enqueueNetworkTaskResult(context.Background(), task, result)
	}
	client.networkResults.mu.Lock()
	for _, item := range client.networkResults.pending {
		item.expiresAt = time.Now().Add(-time.Second)
	}
	client.networkResults.mu.Unlock()
	client.enqueueNetworkTaskResult(context.Background(), task, result)
	if networkPendingCount(client) != 1 {
		t.Fatal("expired results prevented admission of a fresh result")
	}
}
