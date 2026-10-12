package store

import (
	"context"
	"testing"
	"time"
)

func TestSilenceContextPaginationAndExactCausalLinks(t *testing.T) {
	s, rule, node, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		created, err := s.CreateIncidentSilence(ctx, i.ID, randomID(), actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60, Reason: "private reason"}, at.Add(time.Duration(n*2+1)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if n < 2 {
			if _, err := s.CancelIncidentSilence(ctx, i.ID, created.SilenceID, randomID(), actor.ID, 1, at.Add(time.Duration(n*2+2)*time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for n := 0; n < 3; n++ {
		page, err := s.ListIncidentSilences(ctx, i.ID, cursor, 1, at.Add(6*time.Second))
		if err != nil || len(page.Silences) != 1 || page.ActiveSilence == nil || page.IncidentState != "firing" {
			t.Fatalf("page: %+v %v", page, err)
		}
		w := page.Silences[0]
		if seen[w.ID] || w.CreatorName != actor.Username || w.Reason != "private reason" || len(w.Decisions) != 1 {
			t.Fatalf("history: %+v", w)
		}
		seen[w.ID] = true
		cursor = page.NextCursor
	}
	if cursor != "" || len(seen) != 3 {
		t.Fatal("incomplete pagination")
	}
	linked, err := s.ListDeliverySilences(ctx, jobs[0].ID, "", 10, at.Add(6*time.Second))
	if err != nil || len(linked.Silences) != 1 || linked.Silences[0].RecordedAt == nil || linked.ActiveSilence != nil {
		t.Fatalf("retroactive delivery link: %+v %v", linked, err)
	}
	observe(t, s, rule, node, at.Add(66*time.Second), true, true)
	fresh, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(fresh) != 2 {
		t.Fatal(err)
	}
	for _, job := range fresh {
		if job.ID == jobs[0].ID {
			continue
		}
		p, err := s.ListDeliverySilences(ctx, job.ID, "", 10, at.Add(66*time.Second))
		if err != nil || len(p.Silences) != 0 {
			t.Fatal("new job inherited old silence")
		}
	}
	end, err := s.ListIncidentSilences(ctx, i.ID, "", 10, at.Add(65*time.Second))
	if err != nil || end.ActiveSilence != nil {
		t.Fatal("half-open expiry remained active")
	}
}
