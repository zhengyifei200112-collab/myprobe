// Package servicehealth defines the shared current-observation contract for
// private service summaries and durable service alert evaluation.
package servicehealth

import (
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

type Observation struct {
	ScheduledAt time.Time
	ExpiresAt   time.Time
	Result      *httpcheck.Result
}

type Input struct {
	ServiceEnabled   bool
	ExecutionEnabled bool
	AgentAvailable   bool
	LastSeenAt       *time.Time
	ReportSeconds    int
	Capabilities     []string
	IntervalSeconds  int
	Observation      *Observation
}

type Assessment struct {
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	ErrorClass string     `json:"error_class,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

type Summary struct {
	State             string     `json:"state"`
	Policy            string     `json:"policy"`
	RequiredObservers int        `json:"required_observers"`
	Success           int        `json:"success"`
	Failure           int        `json:"failure"`
	Unknown           int        `json:"unknown"`
	CurrentCoverage   *float64   `json:"current_coverage"`
	AsOf              time.Time  `json:"as_of"`
	ValidUntil        *time.Time `json:"valid_until,omitempty"`
}

var failureDescriptions = map[string]string{
	"dns": "DNS 解析失败", "refused": "连接被拒绝", "timeout": "请求超时",
	"tls_invalid": "TLS 验证失败", "certificate_expired": "证书已过期",
	"status_mismatch": "状态码不符合要求", "content_mismatch": "内容断言不符合要求",
	"response_too_large": "响应超过大小限制", "redirect_limit": "重定向超过限制",
}

func FailureDescription(class string) string { return failureDescriptions[class] }

// Evaluate consumes admitted current-revision evidence. Execution/session checks
// describe availability at read time; they never turn absent checks into success.
func Evaluate(v Input, now time.Time) Assessment {
	unknown := func(reason string) Assessment { return Assessment{Status: "unknown", Reason: reason} }
	if !v.ServiceEnabled {
		return unknown("service_disabled")
	}
	if !v.ExecutionEnabled {
		return unknown("execution_disabled")
	}
	onlineFor := time.Duration(max(v.ReportSeconds*3, 20)) * time.Second
	if v.LastSeenAt == nil || v.LastSeenAt.IsZero() || v.LastSeenAt.After(now.Add(30*time.Second)) || now.Sub(*v.LastSeenAt) > onlineFor {
		return unknown("agent_offline")
	}
	if !v2.AllowsHTTP(v.Capabilities) {
		return unknown("unsupported")
	}
	if !v.AgentAvailable {
		return unknown("agent_unavailable")
	}
	if v.Observation == nil {
		return unknown("not_observed")
	}
	o := v.Observation
	if o.Result == nil {
		if now.After(o.ExpiresAt.Add(time.Minute)) {
			return unknown("missing_result")
		}
		return unknown("pending")
	}
	r := o.Result
	freshFor := max(time.Minute, 3*time.Duration(v.IntervalSeconds)*time.Second)
	if o.ScheduledAt.IsZero() || o.ScheduledAt.After(now) || now.Sub(o.ScheduledAt) > freshFor || r.CompletedAt.IsZero() || r.CompletedAt.After(now.Add(30*time.Second)) || now.Sub(r.CompletedAt) > freshFor {
		return unknown("stale_result")
	}
	if r.Outcome == "unobserved" {
		a := unknown("unobserved")
		switch r.ErrorClass {
		case "unsupported", "busy", "cancelled", "invalid_task", "policy_denied", "internal":
			a.ErrorClass = r.ErrorClass
		}
		return a
	}
	a := Assessment{Status: r.Outcome}
	switch r.Outcome {
	case "success":
		if r.ErrorClass != "" || r.StatusCode < 100 || r.StatusCode > 599 {
			return unknown("invalid_result")
		}
	case "failure":
		if FailureDescription(r.ErrorClass) == "" {
			return unknown("invalid_result")
		}
		a.ErrorClass = r.ErrorClass
	default:
		return unknown("invalid_result")
	}
	deadline := minTime(o.ScheduledAt.Add(freshFor), r.CompletedAt.Add(freshFor))
	deadline = minTime(deadline, v.LastSeenAt.Add(onlineFor))
	a.ValidUntil = &deadline
	return a
}

func Summarize(items []Assessment, now time.Time) Summary {
	s := Summary{State: "unknown", Policy: "any_required_observer_failure", RequiredObservers: len(items), AsOf: now.UTC()}
	for _, a := range items {
		switch a.Status {
		case "success":
			s.Success++
		case "failure":
			s.Failure++
		default:
			s.Unknown++
		}
		if (a.Status == "success" || a.Status == "failure") && a.ValidUntil != nil && (s.ValidUntil == nil || a.ValidUntil.Before(*s.ValidUntil)) {
			deadline := *a.ValidUntil
			s.ValidUntil = &deadline
		}
	}
	if len(items) > 0 {
		coverage := float64(s.Success+s.Failure) / float64(len(items))
		s.CurrentCoverage = &coverage
	}
	if s.Failure > 0 {
		s.State = "degraded"
	} else if s.Success == len(items) && len(items) > 0 {
		s.State = "healthy"
	}
	return s
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
