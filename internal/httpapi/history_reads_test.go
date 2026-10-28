package httpapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNodeHistoryReadsOverlapAndPreservePayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var limiter historyReadLimiter
	latencyStarted, trafficStarted := make(chan struct{}), make(chan struct{})
	want := nodeHistoryData{Metrics: []store.MetricHistoryPoint{{CPUPercent: 17}}, Latency: []store.LatencyHistoryPoint{{TargetID: "synthetic-target"}}, Traffic: []store.TrafficHistoryPoint{{RXBytes: 100}}}
	got, stage, err := limiter.nodeHistory(ctx, nodeHistoryReads{
		metrics: func(ctx context.Context) ([]store.MetricHistoryPoint, error) {
			select {
			case <-latencyStarted:
				return want.Metrics, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		latency: func(ctx context.Context) ([]store.LatencyHistoryPoint, error) {
			close(latencyStarted)
			select {
			case <-trafficStarted:
				return want.Latency, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		traffic: func(context.Context) ([]store.TrafficHistoryPoint, error) {
			close(trafficStarted)
			return want.Traffic, nil
		},
	})
	if err != nil || stage != "" || !reflect.DeepEqual(got, want) {
		t.Fatalf("history payload/overlap: %+v stage=%s err=%v", got, stage, err)
	}
}

func TestHistoryReadLimitAppliesAcrossRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var limiter historyReadLimiter
	var active, maximum atomic.Int32
	started, release := make(chan struct{}, 36), make(chan struct{})
	read := func(ctx context.Context) error {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var workers sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		workers.Go(func() {
			_, _, err := limiter.nodeHistory(ctx, nodeHistoryReads{
				metrics: func(ctx context.Context) ([]store.MetricHistoryPoint, error) { return nil, read(ctx) },
				latency: func(ctx context.Context) ([]store.LatencyHistoryPoint, error) { return nil, read(ctx) },
				traffic: func(ctx context.Context) ([]store.TrafficHistoryPoint, error) { return nil, read(ctx) },
			})
			results <- err
		})
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("two history readers did not start")
		}
	}
	close(release)
	workers.Wait()
	for range 12 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 2 || active.Load() != 0 {
		t.Fatalf("shared reader admission: maximum=%d active=%d", maximum.Load(), active.Load())
	}
}

func TestHistoryQueuedCancellationDoesNotStartReadOrLeakSlots(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var limiter historyReadLimiter
	started, release, done := make(chan struct{}, 2), make(chan struct{}), make(chan error, 2)
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	for range 2 {
		go func() {
			done <- limiter.read(ctx, func() error {
				started <- struct{}{}
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			})
		}()
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("two blocking reads did not start")
		}
	}
	queued, stop := context.WithCancel(ctx)
	var calls atomic.Int32
	queuedResult := make(chan error, 1)
	go func() { queuedResult <- limiter.read(queued, func() error { calls.Add(1); return nil }) }()
	stop()
	select {
	case err := <-queuedResult:
		if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
			t.Fatalf("canceled queued read started: calls=%d err=%v", calls.Load(), err)
		}
	case <-ctx.Done():
		t.Fatal("queued cancellation did not return")
	}
	unblock()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if err := limiter.read(ctx, func() error { return nil }); err != nil {
		t.Fatal("reader slot leaked", err)
	}
}

func TestNodeHistoryFailuresCancelAndJoinReaders(t *testing.T) {
	for _, stage := range []string{"metric", "latency", "traffic"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var limiter historyReadLimiter
			started, exited := make(chan struct{}), make(chan struct{})
			failure := errors.New("synthetic read failure")
			got, failedStage, err := limiter.nodeHistory(ctx, nodeHistoryReads{
				metrics: func(ctx context.Context) ([]store.MetricHistoryPoint, error) {
					select {
					case <-started:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					if stage == "metric" {
						return nil, failure
					}
					return []store.MetricHistoryPoint{{CPUPercent: 17}}, nil
				},
				latency: func(ctx context.Context) ([]store.LatencyHistoryPoint, error) {
					close(started)
					defer close(exited)
					if stage == "latency" {
						return nil, failure
					}
					<-ctx.Done()
					return nil, ctx.Err()
				},
				traffic: func(context.Context) ([]store.TrafficHistoryPoint, error) {
					if stage == "traffic" {
						return nil, failure
					}
					return nil, nil
				},
			})
			if failedStage != stage || !errors.Is(err, failure) || !reflect.DeepEqual(got, nodeHistoryData{}) {
				t.Fatalf("failure/partial result: stage=%s err=%v data=%+v", failedStage, err, got)
			}
			select {
			case <-exited:
			default:
				t.Fatal("reader still running after return")
			}
		})
	}
}

func TestNodeHistoryLatencyPanicIsContained(t *testing.T) {
	var limiter historyReadLimiter
	_, stage, err := limiter.nodeHistory(context.Background(), nodeHistoryReads{
		metrics: func(context.Context) ([]store.MetricHistoryPoint, error) { return nil, nil },
		latency: func(context.Context) ([]store.LatencyHistoryPoint, error) { panic("synthetic private detail") },
		traffic: func(context.Context) ([]store.TrafficHistoryPoint, error) { return nil, nil },
	})
	if stage != "latency" || err == nil || strings.Contains(err.Error(), "private detail") {
		t.Fatalf("background panic not contained/redacted: %s %v", stage, err)
	}
}

func TestNodeHistoryMainPanicCancelsAndJoinsExtraReader(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var limiter historyReadLimiter
	started, exited := make(chan struct{}), make(chan struct{})
	func() {
		defer func() {
			if value := recover(); value != "synthetic main panic" {
				t.Errorf("main panic changed: %v", value)
			}
		}()
		_, _, _ = limiter.nodeHistory(ctx, nodeHistoryReads{
			metrics: func(ctx context.Context) ([]store.MetricHistoryPoint, error) {
				select {
				case <-started:
					panic("synthetic main panic")
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
			latency: func(ctx context.Context) ([]store.LatencyHistoryPoint, error) {
				close(started)
				defer close(exited)
				<-ctx.Done()
				return nil, ctx.Err()
			},
			traffic: func(context.Context) ([]store.TrafficHistoryPoint, error) {
				t.Error("traffic ran after metric panic")
				return nil, nil
			},
		})
	}()
	select {
	case <-exited:
	default:
		t.Fatal("extra reader outlived main panic")
	}
	if err := limiter.read(ctx, func() error { return nil }); err != nil {
		t.Fatal("slot leaked after panic", err)
	}
}
