package agentclient

import (
	"context"
	"errors"
	"time"
)

var (
	errJointPreparationTimeout = errors.New("joint load preparation budget exhausted")
	errJointMeasurementTimeout = errors.New("joint load measurement and finalization budget exhausted")
)

// All child processes retain the same cancellation context across the phase
// transition. Resetting the budget must not cancel a live Server or browser.
type jointLoadBudget struct {
	context.Context
	cancel                          context.CancelCauseFunc
	preparation, measurement        *time.Timer
	preparationStarted, started     time.Time
	measurementDuration, finalizing time.Duration
}

func newJointLoadBudget(preparation, measurement, finalizing time.Duration) *jointLoadBudget {
	ctx, cancel := context.WithCancelCause(context.Background())
	b := &jointLoadBudget{Context: ctx, cancel: cancel, preparationStarted: time.Now(), measurementDuration: measurement, finalizing: finalizing}
	b.preparation = time.AfterFunc(preparation, func() { cancel(errJointPreparationTimeout) })
	return b
}

func (b *jointLoadBudget) startMeasurement() (time.Time, error) {
	if !b.started.IsZero() {
		return time.Time{}, errors.New("joint load measurement already started")
	}
	// A false Stop means the preparation callback has fired or is firing. Fail
	// conservatively; never let a late setup callback cancel a new measurement.
	if !b.preparation.Stop() {
		b.cancel(errJointPreparationTimeout)
		return time.Time{}, context.Cause(b.Context)
	}
	if err := context.Cause(b.Context); err != nil {
		return time.Time{}, err
	}
	b.started = time.Now()
	b.measurement = time.AfterFunc(b.measurementDuration+b.finalizing, func() { b.cancel(errJointMeasurementTimeout) })
	return b.started, nil
}

func (b *jointLoadBudget) stop() {
	b.preparation.Stop()
	if b.measurement != nil {
		b.measurement.Stop()
	}
	b.cancel(context.Canceled)
}
