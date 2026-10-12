package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// A real provider holds its response while the management action cancels the
// claimed intent. Its later ACK must remain visible without reviving retries.
func TestSilenceDuringActualSendRetainsLateAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "race.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accepted, release := make(chan struct{}), make(chan struct{})
	released := false
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			t.Error(err)
		}
		if n.State != "firing" || r.Header.Get("Idempotency-Key") == "" {
			t.Error("invalid actual notification")
		}
		close(accepted)
		<-release
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	defer func() {
		if !released {
			close(release)
		}
	}()
	svc := New(db, strings.Repeat("s", 32), NewHTTPSender(receiver.Client()), nil)
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "silence race"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	node = updateNodeExpiry(t, db, node, &expires)
	channel, err := svc.CreateChannel(ctx, "local receiver", "webhook", ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRule(ctx, node.ID, channel.ID, "expiry", RuleConfig{DaysBefore: 1}, 900); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
	if err != nil || len(incidents) != 1 {
		t.Fatal(err)
	}
	i := incidents[0]
	jobs, err := db.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	actor, err := db.CreateUser(ctx, "race fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := svc.DeliverOne(ctx, time.Now().UTC()); done <- err }()
	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("receiver did not accept")
	}
	w, err := db.CreateIncidentSilence(ctx, i.ID, "race-silence", actor.ID, store.CreateIncidentSilenceParams{DurationSeconds: 60}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("worker lost completion")
	}
	job, err := db.Delivery(ctx, jobs[0].ID)
	if err != nil || job.Status != "canceled" || job.ErrorClass != "silence" {
		t.Fatalf("job: %+v %v", job, err)
	}
	attempts, err := db.ListDeliveryAttempts(ctx, job.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Outcome != "delivered" {
		t.Fatalf("late ACK: %+v %v", attempts, err)
	}
	current, err := db.Incident(ctx, i.ID)
	if err != nil || current.State != "firing" || current.LastEnqueuedAt != nil {
		t.Fatal("late ACK changed incident")
	}
	if worked, err := svc.DeliverOne(ctx, w.EndsAt.Add(time.Second)); err != nil || worked {
		t.Fatalf("expiry revived canceled attempt: %v %v", worked, err)
	}
}
