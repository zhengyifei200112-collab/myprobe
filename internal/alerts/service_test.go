package alerts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type recordingSender struct {
	mu        sync.Mutex
	messages  []Notification
	failCount int
}

func (s *recordingSender) Deliver(_ context.Context, _ string, _ ChannelConfig, notification Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, notification)
	if s.failCount > 0 {
		s.failCount--
		return errors.New("receiver unavailable")
	}
	return nil
}

func (s *recordingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

func TestAlertLifecycleDedupCooldownAndResolution(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiresSoon := now.Add(time.Hour)
	node = updateNodeExpiry(t, database, node, &expiresSoon)

	recorder := &recordingSender{}
	service := New(database, strings.Repeat("s", 32), recorder, nil)
	channel, err := service.CreateChannel(ctx, "ops", "webhook", ChannelConfig{URL: "https://example.com/hook"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := service.CreateRule(ctx, node.ID, channel.ID, "expiry", RuleConfig{DaysBefore: 1}, 30)
	if err != nil {
		t.Fatal(err)
	}

	if err := service.Tick(ctx, now); err != nil || recorder.count() != 1 {
		t.Fatalf("first tick count = %d, error = %v", recorder.count(), err)
	}
	if err := service.Tick(ctx, now.Add(10*time.Second)); err != nil || recorder.count() != 1 {
		t.Fatalf("dedupe count = %d, error = %v", recorder.count(), err)
	}
	if err := service.Tick(ctx, now.Add(31*time.Second)); err != nil || recorder.count() != 2 {
		t.Fatalf("cooldown reminder count = %d, error = %v", recorder.count(), err)
	}

	expiresLater := now.Add(10 * 24 * time.Hour)
	_ = updateNodeExpiry(t, database, node, &expiresLater)
	if err := service.Tick(ctx, now.Add(32*time.Second)); err != nil || recorder.count() != 3 {
		t.Fatalf("resolution count = %d, error = %v", recorder.count(), err)
	}
	if recorder.messages[2].State != "resolved" {
		t.Fatalf("resolution = %#v", recorder.messages[2])
	}
	events, err := database.ListAlertEvents(ctx, 10)
	if err != nil || len(events) != 3 || events[0].RuleID != rule.ID || events[0].State != "resolved" {
		t.Fatalf("events = %#v, error = %v", events, err)
	}
}

func TestFailedDeliveryRetriesOnlyAfterCooldown(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "edge"})
	now := time.Now().UTC().Truncate(time.Second)
	expiresSoon := now.Add(time.Hour)
	_ = updateNodeExpiry(t, database, node, &expiresSoon)
	recorder := &recordingSender{failCount: 1}
	service := New(database, strings.Repeat("s", 32), recorder, nil)
	channel, _ := service.CreateChannel(ctx, "ops", "webhook", ChannelConfig{URL: "https://example.com/hook"})
	_, _ = service.CreateRule(ctx, node.ID, channel.ID, "expiry", RuleConfig{DaysBefore: 1}, 30)

	_ = service.Tick(ctx, now)
	_ = service.Tick(ctx, now.Add(20*time.Second))
	if recorder.count() != 1 {
		t.Fatalf("count before cooldown = %d", recorder.count())
	}
	_ = service.Tick(ctx, now.Add(31*time.Second))
	if recorder.count() != 2 {
		t.Fatalf("count after cooldown = %d", recorder.count())
	}
	events, _ := database.ListAlertEvents(ctx, 10)
	if len(events) != 2 || events[1].State != "failed" || events[1].DeliveryError != "receiver unavailable" || events[0].State != "firing" {
		t.Fatalf("events = %#v", events)
	}
}

func TestOfflineEvaluationUsesLastSeenThreshold(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	service := New(database, strings.Repeat("s", 32), &recordingSender{}, nil)
	lastSeen := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	node := store.Node{ID: "node", Name: "edge", CreatedAt: lastSeen.Add(-time.Hour), LastSeenAt: &lastSeen}
	rule := store.AlertRule{Kind: "offline", Config: []byte(`{"offline_seconds":60}`)}
	active, _, known, err := service.evaluate(ctx, rule, node, lastSeen.Add(61*time.Second))
	if err != nil || !known || !active {
		t.Fatalf("active = %v, known = %v, error = %v", active, known, err)
	}
	active, _, _, _ = service.evaluate(ctx, rule, node, lastSeen.Add(30*time.Second))
	if active {
		t.Fatal("node was marked offline before threshold")
	}
}

func TestChangingChannelTypeRequiresNewCredentials(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	service := New(database, strings.Repeat("s", 32), &recordingSender{}, nil)
	channel, err := service.CreateChannel(ctx, "ops", "webhook", ChannelConfig{URL: "https://example.com/hook"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateChannel(ctx, channel.ID, channel.Name, "telegram", nil, true); err == nil {
		t.Fatal("channel type changed without replacement credentials")
	}
	updated, err := service.UpdateChannel(ctx, channel.ID, channel.Name, "telegram", &ChannelConfig{BotToken: "abc:123", ChatID: "-100"}, true)
	if err != nil || updated.Kind != "telegram" {
		t.Fatalf("updated = %#v, error = %v", updated, err)
	}
}

func updateNodeExpiry(t *testing.T, database *store.Store, node store.Node, expiry *time.Time) store.Node {
	t.Helper()
	updated, err := database.UpdateNode(context.Background(), node.ID, store.UpdateNodeParams{
		Name: node.Name, SortOrder: node.SortOrder, Hidden: node.Hidden, Tags: node.Tags,
		CountryCode: node.CountryCode, Currency: node.Currency, PriceMinor: node.PriceMinor,
		BillingCycle: node.BillingCycle, ExpiresAt: expiry, TrafficResetDay: node.TrafficResetDay,
		UseSinceBoot: node.UseSinceBoot, LatencyMode: node.LatencyMode,
		CollectionSeconds: node.CollectionSeconds, ReportSeconds: node.ReportSeconds,
	})
	if err != nil {
		t.Fatal(err)
	}
	return updated
}
