package agentclient

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// Virtual time verifies only budget semantics, never an actual 24-hour soak.
func TestJointLoadBudgetPreservesFullMeasurement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := newJointLoadBudget(2*time.Hour, 24*time.Hour, 3*time.Minute)
		defer budget.stop()
		child, cancel := context.WithCancel(budget.Context)
		defer cancel()
		time.Sleep(119 * time.Minute)
		started, err := budget.startMeasurement()
		if err != nil || started.Sub(budget.preparationStarted) != 119*time.Minute {
			t.Fatal("preparation was not separated", err)
		}
		time.Sleep(24 * time.Hour)
		synctest.Wait()
		if child.Err() != nil {
			t.Fatal("setup consumed measurement or cancelled an existing child")
		}
		if _, err := budget.startMeasurement(); err == nil {
			t.Fatal("second transition reset the measurement budget")
		}
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		if !errors.Is(context.Cause(child), errJointMeasurementTimeout) {
			t.Fatal("measurement/finalization bound not enforced", context.Cause(child))
		}
	})
}

func TestJointLoadBudgetRejectsExpiredPreparation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := newJointLoadBudget(time.Minute, 24*time.Hour, 3*time.Minute)
		defer budget.stop()
		time.Sleep(time.Minute)
		synctest.Wait()
		if _, err := budget.startMeasurement(); !errors.Is(err, errJointPreparationTimeout) {
			t.Fatal("expired setup granted a fresh measurement", err)
		}
	})
}

func TestJointLoadBudgetStopCancelsExistingChildren(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := newJointLoadBudget(time.Hour, time.Hour, time.Minute)
		child, cancel := context.WithCancel(budget.Context)
		defer cancel()
		budget.stop()
		if child.Err() != context.Canceled {
			t.Fatal("explicit stop left children running")
		}
		if _, err := budget.startMeasurement(); err == nil {
			t.Fatal("stopped budget restarted")
		}
	})
}
