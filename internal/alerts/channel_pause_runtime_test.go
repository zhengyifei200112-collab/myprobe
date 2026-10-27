package alerts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func queuePauseSingle(t *testing.T, db *store.Store, svc *Service, channel string, at time.Time) store.NotificationDelivery {
	t.Helper()
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "mixed queue"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := svc.CreateRule(ctx, node.ID, channel, "cpu", RuleConfig{ThresholdPercent: 90}, 300)
	if err != nil {
		t.Fatal(err)
	}
	i, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: at, Known: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal("single queue fixture")
	}
	return jobs[0]
}

func TestChannelPauseRealReceiverMixedQueuesAndManualTests(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	db, svc, at := providerRuntime(t, NewHTTPSender(receiver.Client()), "webhook", ChannelConfig{URL: receiver.URL}, 11)
	channels, err := db.ListNotificationChannels(ctx)
	if err != nil || len(channels) != 1 {
		t.Fatal(err)
	}
	c := channels[0]
	single := queuePauseSingle(t, db, svc, c.ID, at)
	if worked, err := svc.DeliverOne(ctx, at); err != nil || !worked {
		t.Fatal(err)
	}
	c, err = db.NotificationChannel(ctx, c.ID)
	if err != nil || c.DeliveryPausedUntil == nil || c.DeliveryPausedUntil.Before(at.Add(120*time.Second)) {
		t.Fatal("provider delay not persisted")
	}
	if worked, err := svc.DeliverOne(ctx, at.Add(time.Second)); err != nil || worked {
		t.Fatal("same-channel work bypassed pause")
	}
	if err := svc.TestChannel(ctx, c.ID, at.Add(time.Second)); !errors.Is(err, store.ErrChannelRateLimited) {
		t.Fatal("channel test bypassed pause")
	}
	template, err := db.SaveNotificationTemplate(ctx, "", "pause fixture", "all", "test", "body")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.TestTemplate(ctx, template.ID, c.ID, at.Add(time.Second)); !errors.Is(err, store.ErrChannelRateLimited) {
		t.Fatal("template test bypassed pause")
	}
	if calls.Load() != 1 {
		t.Fatal("paused work made a request")
	}
	queued, err := db.Delivery(ctx, single.ID)
	if err != nil || queued.AttemptCount != 0 || queued.AttemptSequence != 0 {
		t.Fatal("waiting consumed a reservation")
	}
	for n := 0; n < 3; n++ {
		if worked, err := svc.DeliverOne(ctx, *c.DeliveryPausedUntil); err != nil || !worked {
			t.Fatalf("due work %d: %v %v", n, worked, err)
		}
	}
	if calls.Load() != 4 {
		t.Fatal("due aggregate/single work missing")
	}
	if err := svc.TestTemplate(ctx, template.ID, c.ID, *c.DeliveryPausedUntil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatal("due template test missing")
	}
	page, err := db.ListNotificationAggregates(ctx, 0, 25)
	if err != nil || page.Aggregates[0].Totals.Delivered+page.Aggregates[1].Totals.Delivered != 11 {
		t.Fatal("mixed group delivery incomplete")
	}
}

func TestChannelPauseConcurrentResponsesDoNotShortenWait(t *testing.T) {
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if r.URL.Path == "/independent" {
			w.WriteHeader(204)
			return
		}
		if n == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Header().Set("Retry-After", "120")
		} else {
			w.Header().Set("Retry-After", "240")
		}
		w.WriteHeader(429)
	}))
	defer receiver.Close()
	defer close(release)
	db, svc, at := providerRuntime(t, NewHTTPSender(receiver.Client()), "webhook", ChannelConfig{URL: receiver.URL}, 11)
	channels, _ := db.ListNotificationChannels(ctx)
	c := channels[0]
	queuePauseSingle(t, db, svc, c.ID, at)
	done := make(chan error, 1)
	go func() { _, err := svc.DeliverOne(ctx, at); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first receiver not entered")
	}
	if worked, err := svc.DeliverOne(ctx, at); err != nil || !worked {
		t.Fatal(err)
	}
	// Unblock without closing twice in cleanup.
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first sender did not finish")
	}
	c, err := db.NotificationChannel(ctx, c.ID)
	if err != nil || c.DeliveryPausedUntil == nil || c.DeliveryPausedUntil.Before(at.Add(240*time.Second)) {
		t.Fatal("shorter late response erased longer wait")
	}
	if worked, err := svc.DeliverOne(ctx, at.Add(130*time.Second)); err != nil || worked {
		t.Fatal("shared wait shortened")
	}
	other, err := svc.CreateChannel(ctx, "independent", "webhook", ChannelConfig{URL: receiver.URL + "/independent"})
	if err != nil {
		t.Fatal(err)
	}
	queuePauseSingle(t, db, svc, other.ID, at)
	if worked, err := svc.DeliverOne(ctx, at.Add(time.Second)); err != nil || !worked || calls.Load() != 3 {
		t.Fatal("unrelated channel was blocked")
	}
}

func TestChannelPauseOutcomeUsesProviderDelayAndRateFallback(t *testing.T) {
	at := time.Now().UTC()
	for _, test := range []struct {
		err      *DeliveryError
		pause    bool
		duration time.Duration
	}{
		{&DeliveryError{Class: "http_rate_limited"}, true, 0},
		{&DeliveryError{Class: "http_server_error", RetryAfter: time.Second}, true, time.Second},
		{&DeliveryError{Class: "http_server_error"}, false, 0},
		{&DeliveryError{Class: "http_client_error", Permanent: true, RetryAfter: time.Hour}, false, 0},
		{&DeliveryError{Class: "transport_error", Ambiguous: true, RetryAfter: time.Hour}, false, 0},
	} {
		outcome := deliveryOutcome(test.err, 1, at)
		if outcome.ChannelRetryAt.IsZero() == test.pause {
			t.Fatal("wrong shared pause eligibility")
		}
		if test.duration > 0 && !outcome.ChannelRetryAt.Equal(at.Add(test.duration)) {
			t.Fatal("provider pause replaced by leaf backoff")
		}
		if test.pause && test.duration == 0 && (outcome.ChannelRetryAt.Before(at.Add(30*time.Second)) || outcome.ChannelRetryAt.After(at.Add(36*time.Second))) {
			t.Fatal("429 without delay has no bounded fallback")
		}
	}
}
