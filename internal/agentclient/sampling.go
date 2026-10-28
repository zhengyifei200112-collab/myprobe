package agentclient

import (
	"context"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type metricsSource interface {
	Hello(context.Context, string, int, int) (protocol.Hello, error)
	Collect(context.Context) (protocol.Report, error)
	UpdateConfig(collector.Config)
}
type collectedSample struct {
	report         protocol.Report
	id, generation uint64
}

// Apply periods as one managed change. A sample collected under an earlier
// generation cannot be emitted as evidence for newly acknowledged settings.
func (c *Client) applySamplingPeriods(collectionSeconds, reportSeconds int) error {
	if collectionSeconds < 1 || collectionSeconds > 3600 || reportSeconds < 1 || reportSeconds > 3600 {
		return errors.New("invalid_sampling_periods")
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	c.applySamplingLocked(time.Duration(collectionSeconds)*time.Second, time.Duration(reportSeconds)*time.Second)
	return nil
}

// settingsMu must be held so partially omitted legacy settings retain the exact
// local duration, including fractions of a second.
func (c *Client) applySamplingLocked(collection, report time.Duration) {
	collectionPeriod, reportPeriod := int64(collection), int64(report)
	if c.collectionNS.Load() == collectionPeriod && c.reportNS.Load() == reportPeriod {
		return
	}
	c.sampleMu.Lock()
	c.sampleGeneration.Add(1)
	c.collectionNS.Store(collectionPeriod)
	c.reportNS.Store(reportPeriod)
	c.sample = nil
	c.sampleMu.Unlock()
	select {
	case c.collectionChanged <- struct{}{}:
	default:
	}
	select {
	case c.reportChanged <- struct{}{}:
	default:
	}
}

// Missing period fields in an older, unnegotiated server message retain local
// defaults. The negotiated managed extension always requires both exact values.
func (c *Client) applyLegacySampling(config protocol.Config) error {
	if config.CollectionSeconds == 0 && config.ReportSeconds == 0 {
		return nil
	}
	for _, seconds := range []int{config.CollectionSeconds, config.ReportSeconds} {
		if seconds != 0 && (seconds < 1 || seconds > 3600) {
			return errors.New("invalid_sampling_periods")
		}
	}
	c.settingsMu.Lock()
	defer c.settingsMu.Unlock()
	collection, report := time.Duration(c.collectionNS.Load()), time.Duration(c.reportNS.Load())
	if config.CollectionSeconds != 0 {
		collection = time.Duration(config.CollectionSeconds) * time.Second
	}
	if config.ReportSeconds != 0 {
		report = time.Duration(config.ReportSeconds) * time.Second
	}
	c.applySamplingLocked(collection, report)
	return nil
}
func (c *Client) collectionLoop(ctx context.Context) {
	for ctx.Err() == nil {
		started := time.Now()
		c.sampleMu.Lock()
		period := time.Duration(c.collectionNS.Load())
		generation := c.sampleGeneration.Load()
		c.sampleMu.Unlock()
		collectCtx, cancel := context.WithTimeout(ctx, min(period, 15*time.Second))
		report, err := c.collector.Collect(collectCtx)
		cancel()
		if err == nil {
			c.sampleMu.Lock()
			if generation == c.sampleGeneration.Load() {
				c.sampleSequence++
				c.sample = &collectedSample{report: report, id: c.sampleSequence, generation: generation}
			}
			c.sampleMu.Unlock()
		} else if ctx.Err() == nil {
			c.logger.Warn("collect metrics", "error", err)
		}
		// Collection time is part of the interval. Work stays sequential; a slow
		// collection does not launch overlapping collectors or replay missed ticks.
		timer := time.NewTimer(max(0, time.Until(started.Add(period))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-c.collectionChanged:
			timer.Stop()
		case <-timer.C:
		}
	}
}
func (c *Client) currentSample() (collectedSample, bool) {
	c.sampleMu.Lock()
	defer c.sampleMu.Unlock()
	if c.sample == nil || c.sample.generation != c.sampleGeneration.Load() {
		return collectedSample{}, false
	}
	return *c.sample, true
}
