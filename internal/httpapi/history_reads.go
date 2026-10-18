package httpapi

import (
	"context"
	"errors"
	"sync"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// Limit these node-history queries across all requests on this Server. The shared
// four-connection Store still has room for health, reports and worker activity.
// A zero value is usable by both the production constructor and test fixtures.
type historyReadLimiter struct {
	once  sync.Once
	slots chan struct{}
}

func (l *historyReadLimiter) read(ctx context.Context, call func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.once.Do(func() { l.slots = make(chan struct{}, 2) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case l.slots <- struct{}{}:
	}
	defer func() { <-l.slots }()
	if err := ctx.Err(); err != nil {
		return err
	}
	return call()
}

type nodeHistoryData struct {
	Metrics []store.MetricHistoryPoint
	Latency []store.LatencyHistoryPoint
	Traffic []store.TrafficHistoryPoint
}

type nodeHistoryReads struct {
	metrics func(context.Context) ([]store.MetricHistoryPoint, error)
	latency func(context.Context) ([]store.LatencyHistoryPoint, error)
	traffic func(context.Context) ([]store.TrafficHistoryPoint, error)
}

// Overlap latency with metric/traffic reads, with at most two queries admitted
// globally. Always join the extra reader before returning or unwinding a panic.
// The three independent read snapshots have the same semantics as the previous
// sequential calls; each receives the same already-parsed absolute window.
func (l *historyReadLimiter) nodeHistory(parent context.Context, reads nodeHistoryReads) (nodeHistoryData, string, error) {
	var data nodeHistoryData
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var latencyErr error
	go func() {
		defer close(done)
		defer func() {
			if recover() != nil {
				// A reader panic must not escape a request goroutine and terminate
				// the process, or expose internal values in an API response.
				latencyErr = errors.New("latency history reader panicked")
			}
		}()
		latencyErr = l.read(ctx, func() error {
			var err error
			data.Latency, err = reads.latency(ctx)
			return err
		})
	}()
	defer func() { cancel(); <-done }()
	if err := l.read(ctx, func() error {
		var err error
		data.Metrics, err = reads.metrics(ctx)
		return err
	}); err != nil {
		return nodeHistoryData{}, "metric", err
	}
	trafficErr := l.read(ctx, func() error {
		var err error
		data.Traffic, err = reads.traffic(ctx)
		return err
	})
	if trafficErr != nil {
		cancel()
	}
	<-done
	if latencyErr != nil && !(trafficErr != nil && errors.Is(latencyErr, context.Canceled)) {
		return nodeHistoryData{}, "latency", latencyErr
	}
	if trafficErr != nil {
		return nodeHistoryData{}, "traffic", trafficErr
	}
	return data, "", nil
}
