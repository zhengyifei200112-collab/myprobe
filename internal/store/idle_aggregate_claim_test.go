package store

import (
	"context"
	"testing"
	"time"
)

func TestIdleAggregateClaimDoesNotWaitForWriter(t *testing.T) {
	s := idleDeliveryFixture(t)
	release := lockIdleDeliveryWriter(t, s)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job, err := s.ClaimAggregate(ctx, time.Now().UTC(), time.Minute)
	if err != nil || job != nil {
		t.Fatalf("idle aggregate requested writer: job=%v error=%v", job, err)
	}
}

func TestSingleClaimIgnoresAggregateMembersBeforeWriter(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	release := lockIdleDeliveryWriter(t, s)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job, err := s.ClaimDelivery(ctx, at.Add(time.Minute), time.Minute)
	if err != nil || job != nil {
		t.Fatalf("aggregate members occupied single-delivery writer: job=%v error=%v", job, err)
	}
}

func TestAggregateFutureAndLeaseStillBecomeClaimable(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	ctx := context.Background()
	release := lockIdleDeliveryWriter(t, s)
	limited, cancel := context.WithTimeout(ctx, time.Second)
	job, err := s.ClaimAggregate(limited, at, time.Minute)
	cancel()
	release()
	if err != nil || job != nil {
		t.Fatalf("future aggregate requested writer: job=%v error=%v", job, err)
	}
	job, err = s.ClaimAggregate(ctx, at.Add(30*time.Second), time.Minute)
	if err != nil || job == nil {
		t.Fatalf("due aggregate was skipped: job=%v error=%v", job, err)
	}
	release = lockIdleDeliveryWriter(t, s)
	limited, cancel = context.WithTimeout(ctx, time.Second)
	early, err := s.ClaimAggregate(limited, job.LeaseUntil.Add(-time.Millisecond), time.Minute)
	cancel()
	release()
	if err != nil || early != nil {
		t.Fatalf("unexpired aggregate requested writer: job=%v error=%v", early, err)
	}
	next, err := s.ClaimAggregate(ctx, *job.LeaseUntil, time.Minute)
	if err != nil || next == nil || next.ID != job.ID || next.LeaseToken == job.LeaseToken {
		t.Fatalf("expired aggregate did not recover the same job: next=%v error=%v", next, err)
	}
}
