package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

type aggregateFixtureMember struct {
	rule     AlertRule
	node     Node
	incident *Incident
}

func aggregateFixture(t *testing.T, count int) (*Store, []aggregateFixtureMember, AlertPolicy, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "aggregates.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	channel, err := s.CreateNotificationChannel(ctx, "fixture", "webhook", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]Node{}
	for n := 0; n < count; n++ {
		node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: fmt.Sprintf("fixture-%02d", n)})
		if err != nil {
			t.Fatal(err)
		}
		nodes[node.ID] = node
	}
	p, err := s.SaveAlertPolicy(ctx, AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Scope: alertpolicy.Scope{Kind: "all"}, Enabled: true}, Name: "cpu", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90,"recovery_seconds":0,"aggregation_seconds":30}`), CooldownSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := s.SyncAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	members := []aggregateFixtureMember{}
	for _, r := range rules {
		node := nodes[r.NodeID]
		i, err := s.ObserveAlert(ctx, r, node, AlertObservation{At: at, Known: true, Active: true, Message: strings.Repeat("界", 100)})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, aggregateFixtureMember{r, node, i})
	}
	if len(members) != count {
		t.Fatal("fixture member count")
	}
	return s, members, p, at
}
func claimAggregate(t *testing.T, s *Store, at time.Time) *NotificationAggregate {
	t.Helper()
	g, err := s.ClaimAggregate(context.Background(), at, time.Minute)
	if err != nil || g == nil {
		t.Fatalf("claim: %+v %v", g, err)
	}
	return g
}
func prepareAggregate(t *testing.T, s *Store, g *NotificationAggregate, at time.Time) AggregateSend {
	t.Helper()
	send, err := s.PrepareAggregateSend(context.Background(), g.ID, g.LeaseToken, at)
	if err != nil {
		t.Fatal(err)
	}
	return send
}

func TestAggregateWindowShardsAndFinalMemberValidation(t *testing.T) {
	s, members, _, at := aggregateFixture(t, 11)
	ctx := context.Background()
	if rowCount(t, s, "notification_aggregates") != 2 || rowCount(t, s, "notification_deliveries") != 11 {
		t.Fatal("unbounded or missing membership")
	}
	if g, err := s.ClaimAggregate(ctx, at.Add(29*time.Second), time.Minute); err != nil || g != nil {
		t.Fatal("window sent early")
	}
	if solo, err := s.ClaimDelivery(ctx, at.Add(time.Hour), time.Minute); err != nil || solo != nil {
		t.Fatal("single worker stole aggregate work")
	}
	g := claimAggregate(t, s, at.Add(30*time.Second))
	selected := []aggregateFixtureMember{}
	for _, m := range members {
		jobs, err := s.ListIncidentDeliveries(ctx, m.incident.ID, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if jobs[0].AggregateID == g.ID {
			selected = append(selected, m)
		}
	}
	if len(selected) != 10 {
		t.Fatal("wrong shard size")
	}
	actor, err := s.CreateUser(ctx, "fixture operator", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIncidentSilence(ctx, selected[0].incident.ID, "mute", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, at.Add(31*time.Second)); err != nil {
		t.Fatal(err)
	}
	observe(t, s, selected[1].rule, selected[1].node, at.Add(31*time.Second), true, false)
	observe(t, s, selected[2].rule, selected[2].node, at.Add(31*time.Second), false, false)
	send := prepareAggregate(t, s, g, at.Add(32*time.Second))
	var n AggregateNotification
	if err := json.Unmarshal(send.Payload, &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Members) != 7 || len(n.Message) > AggregateMessageByteLimit || len(send.Payload) > AggregatePayloadByteLimit || strings.Contains(n.Message, "\ufffd") {
		t.Fatalf("bounded body: %+v", n)
	}
	for _, ref := range n.Members {
		for _, dropped := range selected[:3] {
			if ref.IncidentID == dropped.incident.ID {
				t.Fatal("inapplicable member leaked into old summary")
			}
		}
	}
	if _, err := s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at.Add(32*time.Second)); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatal("attempt prepared twice")
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(33*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	for index, m := range selected {
		jobs, err := s.ListIncidentDeliveries(ctx, m.incident.ID, 0, 10)
		if err != nil || len(jobs) != 1 {
			t.Fatalf("pre-send recovery produced a standalone message: %+v %v", jobs, err)
		}
		want := "delivered"
		if index < 2 {
			want = "canceled"
		} else if index == 2 {
			want = "pending"
		}
		if jobs[0].Status != want {
			t.Fatalf("member%d status %+v", index, jobs[0])
		}
		if index < 3 {
			attempts, err := s.ListDeliveryAttempts(ctx, jobs[0].ID)
			if err != nil || len(attempts) != 1 || attempts[0].Outcome != "canceled" {
				t.Fatalf("known unsent: %+v %v", attempts, err)
			}
		}
	}
	other := claimAggregate(t, s, at.Add(33*time.Second))
	if other.ID == g.ID {
		t.Fatal("stale member blocked another shard")
	}
	prepareAggregate(t, s, other, at.Add(33*time.Second))
	if err := s.CompleteAggregate(ctx, other.ID, other.LeaseToken, at.Add(34*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	observe(t, s, selected[2].rule, selected[2].node, at.Add(48*time.Second), true, true)
	fresh := claimAggregate(t, s, at.Add(48*time.Second))
	if fresh.ID != g.ID {
		t.Fatal("lost deferred aggregate member")
	}
	current := prepareAggregate(t, s, fresh, at.Add(48*time.Second))
	if current.IdempotencyKey == send.IdempotencyKey {
		t.Fatal("changed body reused old key")
	}
	if err := s.CompleteAggregate(ctx, fresh.ID, fresh.LeaseToken, at.Add(49*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateRetryFreezesPayloadAndRespectsDeadline(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 3)
	ctx := context.Background()
	g := claimAggregate(t, s, at.Add(30*time.Second))
	first := prepareAggregate(t, s, g, at.Add(30*time.Second))
	failure := DeliveryOutcome{ErrorClass: "http_server_error", RetryAt: at.Add(40 * time.Second)}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second), failure); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(32*time.Second), failure); err != nil {
		t.Fatal("committed result did not replay", err)
	}
	if g, err := s.ClaimAggregate(ctx, at.Add(39*time.Second), time.Minute); err != nil || g != nil {
		t.Fatal("retry before deadline")
	}
	retry := claimAggregate(t, s, at.Add(40*time.Second))
	second := prepareAggregate(t, s, retry, at.Add(40*time.Second))
	if !reflect.DeepEqual(first.Payload, second.Payload) || first.IdempotencyKey != second.IdempotencyKey {
		t.Fatal("unchanged retry body/key drifted")
	}
	if err := s.CompleteAggregate(ctx, retry.ID, retry.LeaseToken, at.Add(41*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(42*time.Second), DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatal("conflicting historical result overwritten")
	}
	if rowCount(t, s, "notification_aggregate_attempts") != 2 || rowCount(t, s, "delivery_attempts") != 6 || rowCount(t, s, "alert_events") != 6 {
		t.Fatal("per-event retry history lost or duplicated")
	}
}

func TestConcurrentAggregateClaimAndAtomicCompletion(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 3)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan *NotificationAggregate, 16)
	failures := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			g, err := s.ClaimAggregate(ctx, at.Add(30*time.Second), time.Minute)
			if err != nil {
				failures <- err
			}
			if g != nil {
				results <- g
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var claimed *NotificationAggregate
	for g := range results {
		if claimed != nil {
			t.Fatal("multiple aggregate owners")
		}
		claimed = g
	}
	if claimed == nil {
		t.Fatal("no owner")
	}
	prepareAggregate(t, s, claimed, at.Add(30*time.Second))
	if _, err := s.db.Exec(`CREATE TRIGGER fail_group_history BEFORE INSERT ON alert_events WHEN (SELECT count(*) FROM alert_events)=1 BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAggregate(ctx, claimed.ID, claimed.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{Delivered: true}); err == nil {
		t.Fatal("partial completion accepted")
	}
	if rowCount(t, s, "alert_events") != 0 {
		t.Fatal("partial legacy history")
	}
	var delivered int
	if err := s.db.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE status='delivered'`).Scan(&delivered); err != nil || delivered != 0 {
		t.Fatal("partial member status")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_group_history`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAggregate(ctx, claimed.ID, claimed.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if rowCount(t, s, "alert_events") != 3 || rowCount(t, s, "notification_aggregate_attempts") != 1 {
		t.Fatal("duplicate atomic completion")
	}
}

func TestAggregatePreparedAndStartedLeaseRecovery(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepared", true: "authorized-send"}[sent], func(t *testing.T) {
			s, _, _, at := aggregateFixture(t, 2)
			ctx := context.Background()
			g, err := s.ClaimAggregate(ctx, at.Add(30*time.Second), 10*time.Second)
			if err != nil || g == nil {
				t.Fatal(err)
			}
			var original AggregateSend
			if sent {
				original = prepareAggregate(t, s, g, at.Add(30*time.Second))
			}
			retry := claimAggregate(t, s, at.Add(40*time.Second))
			next := prepareAggregate(t, s, retry, at.Add(40*time.Second))
			if sent && original.IdempotencyKey != next.IdempotencyKey {
				t.Fatal("lost-result replay changed key")
			}
			var previous string
			if err := s.db.QueryRow(`SELECT outcome FROM notification_aggregate_attempts WHERE aggregate_id=? AND attempt_number=1`, g.ID).Scan(&previous); err != nil {
				t.Fatal(err)
			}
			want := "canceled"
			if sent {
				want = "unknown"
			}
			if previous != want {
				t.Fatalf("lease history %s want %s", previous, want)
			}
			if err := s.CompleteAggregate(ctx, retry.ID, retry.LeaseToken, at.Add(41*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
