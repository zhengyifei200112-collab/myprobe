package servicehealth

import (
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestCurrentServiceHealthPolicyAndEmptyCoverage(t *testing.T) {
	now := time.Now().UTC()
	for _, v := range []struct {
		name                      string
		states                    []string
		state                     string
		success, failure, unknown int
		coverage                  float64
	}{
		{"empty", nil, "unknown", 0, 0, 0, 0},
		{"all-success", []string{"success", "success"}, "healthy", 2, 0, 0, 1},
		{"success-missing", []string{"success", "unknown"}, "unknown", 1, 0, 1, .5},
		{"failure-missing", []string{"failure", "unknown"}, "degraded", 0, 1, 1, .5},
		{"mixed", []string{"success", "failure", "unknown"}, "degraded", 1, 1, 1, 2.0 / 3},
		{"all-missing", []string{"unknown", "unknown"}, "unknown", 0, 0, 2, 0},
		{"unexpected", []string{"success", "unexpected"}, "unknown", 1, 0, 1, .5},
	} {
		t.Run(v.name, func(t *testing.T) {
			items := make([]Assessment, len(v.states))
			for i, state := range v.states {
				items[i] = Assessment{Status: state}
			}
			s := Summarize(items, now)
			if s.State != v.state || s.Policy != "any_required_observer_failure" || s.RequiredObservers != len(items) || s.Success != v.success || s.Failure != v.failure || s.Unknown != v.unknown || !s.AsOf.Equal(now) {
				t.Fatalf("summary mismatch: %+v", s)
			}
			if len(items) == 0 {
				if s.CurrentCoverage != nil {
					t.Fatal("empty observers manufactured coverage")
				}
			} else if s.CurrentCoverage == nil || *s.CurrentCoverage != v.coverage {
				t.Fatal("coverage mismatch")
			}
		})
	}
	earlier, later := now.Add(time.Second), now.Add(2*time.Second)
	s := Summarize([]Assessment{{Status: "success", ValidUntil: &later}, {Status: "failure", ValidUntil: &earlier}}, now)
	if s.ValidUntil == nil || !s.ValidUntil.Equal(earlier) {
		t.Fatal("earliest expiring evidence lost")
	}
}

func TestCurrentObserverEvidenceBoundaries(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"success", "failure", "disabled-service", "disabled-execution", "offline", "online-boundary", "future-host", "future-host-boundary", "no-metadata", "unavailable", "missing", "pending", "pending-grace-boundary", "missing-result", "stale-plan", "freshness-boundary", "stale-completion", "future-completion", "future-completion-boundary", "future-plan", "unobserved", "invalid-class", "invalid-status", "empty-timestamp"} {
		t.Run(mode, func(t *testing.T) {
			seen := now
			result := &httpcheck.Result{Outcome: "success", StatusCode: 200, CompletedAt: now}
			o := &Observation{ScheduledAt: now, ExpiresAt: now.Add(time.Second), Result: result}
			input := Input{ServiceEnabled: true, ExecutionEnabled: true, AgentAvailable: true, LastSeenAt: &seen, ReportSeconds: 5, Capabilities: []string{"http_probe.v1"}, IntervalSeconds: 30, Observation: o}
			want, reason := "unknown", ""
			switch mode {
			case "success":
				want = "success"
			case "failure":
				result.Outcome = "failure"
				result.StatusCode = 503
				result.ErrorClass = "status_mismatch"
				want = "failure"
			case "disabled-service":
				input.ServiceEnabled = false
				reason = "service_disabled"
			case "disabled-execution":
				input.ExecutionEnabled = false
				reason = "execution_disabled"
			case "offline":
				seen = now.Add(-20*time.Second - time.Nanosecond)
				reason = "agent_offline"
			case "online-boundary":
				seen = now.Add(-20 * time.Second)
				want = "success"
			case "future-host":
				seen = now.Add(30*time.Second + time.Nanosecond)
				reason = "agent_offline"
			case "future-host-boundary":
				seen = now.Add(30 * time.Second)
				want = "success"
			case "no-metadata":
				input.Capabilities = nil
				reason = "unsupported"
			case "unavailable":
				input.AgentAvailable = false
				reason = "agent_unavailable"
			case "missing":
				input.Observation = nil
				reason = "not_observed"
			case "pending":
				o.Result = nil
				reason = "pending"
			case "pending-grace-boundary":
				o.Result = nil
				o.ExpiresAt = now.Add(-time.Minute)
				reason = "pending"
			case "missing-result":
				o.Result = nil
				o.ExpiresAt = now.Add(-time.Minute - time.Nanosecond)
				reason = "missing_result"
			case "stale-plan":
				o.ScheduledAt = now.Add(-90*time.Second - time.Nanosecond)
				reason = "stale_result"
			case "freshness-boundary":
				o.ScheduledAt = now.Add(-90 * time.Second)
				result.CompletedAt = o.ScheduledAt
				want = "success"
			case "stale-completion":
				result.CompletedAt = now.Add(-90*time.Second - time.Nanosecond)
				reason = "stale_result"
			case "future-completion":
				result.CompletedAt = now.Add(30*time.Second + time.Nanosecond)
				reason = "stale_result"
			case "future-completion-boundary":
				result.CompletedAt = now.Add(30 * time.Second)
				want = "success"
			case "future-plan":
				o.ScheduledAt = now.Add(time.Nanosecond)
				reason = "stale_result"
			case "unobserved":
				result.Outcome = "unobserved"
				result.StatusCode = 0
				result.ErrorClass = "policy_denied"
				reason = "unobserved"
			case "invalid-class":
				result.Outcome = "failure"
				result.ErrorClass = "private-marker"
				reason = "invalid_result"
			case "invalid-status":
				result.StatusCode = 0
				reason = "invalid_result"
			case "empty-timestamp":
				result.CompletedAt = time.Time{}
				reason = "stale_result"
			}
			a := Evaluate(input, now)
			if a.Status != want || a.Reason != reason {
				t.Fatalf("got %+v want %s/%s", a, want, reason)
			}
			if strings.Contains(a.ErrorClass, "private-marker") {
				t.Fatal("untrusted classification exposed")
			}
			if want == "unknown" && a.ValidUntil != nil {
				t.Fatal("unknown evidence received a success lease")
			}
			if want != "unknown" && (a.ValidUntil == nil || a.ValidUntil.Before(now)) {
				t.Fatal("freshness deadline missing")
			}
		})
	}
}
