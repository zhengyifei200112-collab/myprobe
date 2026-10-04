package httpcheck

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestTaskAndResultBinding(t *testing.T) {
	now := time.Now().UTC()
	task := Task{ID: "task", ServiceID: "service", Revision: 2, ScheduledAt: now, ExpiresAt: now.Add(time.Minute), Spec: Spec{URL: "https://example.com", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}}
	if err := task.Validate(now); err != nil {
		t.Fatal(err)
	}
	result := Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: 2, ScheduledAt: now, CompletedAt: now.Add(time.Second), Outcome: "success", StatusCode: 200, DurationMS: 1000}
	result.Certificates = []Certificate{{SHA256: strings.Repeat("ab", 32), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Verified: true}}
	if err := result.ValidateFor(task, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Result){
		func(r *Result) { r.TaskID = "other" }, func(r *Result) { r.ServiceID = "other" }, func(r *Result) { r.Revision++ }, func(r *Result) { r.ScheduledAt = r.ScheduledAt.Add(time.Second) },
		func(r *Result) { r.CompletedAt = task.ExpiresAt.Add(time.Nanosecond) }, func(r *Result) { r.CompletedAt = now.Add(-time.Second) },
		func(r *Result) { r.DurationMS = math.NaN() }, func(r *Result) { r.DurationMS = math.Inf(1) }, func(r *Result) { r.DurationMS = 5001 },
		func(r *Result) { r.StatusCode = 500 }, func(r *Result) { r.ErrorClass = "timeout" },
	} {
		copy := result
		mutate(&copy)
		if copy.ValidateFor(task, now.Add(time.Second)) == nil {
			t.Errorf("accepted invalid result: %+v", copy)
		}
	}
	if result.ValidateFor(task, task.ExpiresAt.Add(time.Minute+time.Nanosecond)) == nil {
		t.Fatal("late result accepted")
	}
	for _, class := range []string{"busy", "unsupported", "policy_denied", "internal", "cancelled"} {
		copy := result
		copy.Outcome = "unobserved"
		copy.ErrorClass = class
		copy.StatusCode = 0
		if err := copy.ValidateFor(task, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		copy.Outcome = "failure"
		if copy.ValidateFor(task, now.Add(time.Second)) == nil {
			t.Fatalf("%s counted as service failure", class)
		}
	}
	if task.Validate(task.ExpiresAt) == nil {
		t.Fatal("expired task accepted")
	}
}

func TestCertificateAndFailureConsistency(t *testing.T) {
	now := time.Now().UTC()
	task := Task{ID: "task", ServiceID: "service", Revision: 1, ScheduledAt: now, ExpiresAt: now.Add(time.Minute), Spec: Spec{URL: "https://example.com", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}}
	r := Result{TaskID: "task", ServiceID: "service", Revision: 1, ScheduledAt: now, CompletedAt: now, Outcome: "success", StatusCode: 200}
	if r.ValidateFor(task, now) == nil {
		t.Fatal("HTTPS success without evidence")
	}
	certificate := Certificate{SHA256: strings.Repeat("01", 32), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), Verified: true}
	r.Certificates = []Certificate{certificate}
	if err := r.ValidateFor(task, now); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Certificate){func(c *Certificate) { c.SHA256 = "bad" }, func(c *Certificate) { c.Verified = false }, func(c *Certificate) { c.NotAfter = c.NotBefore }} {
		copy := certificate
		mutate(&copy)
		r.Certificates = []Certificate{copy}
		if r.ValidateFor(task, now) == nil {
			t.Fatal("invalid certificate accepted")
		}
	}
	r.Certificates = []Certificate{certificate, certificate}
	if r.ValidateFor(task, now) == nil {
		t.Fatal("too many handshakes accepted")
	}
	r.Certificates = nil
	r.Outcome = "failure"
	r.ErrorClass = "status_mismatch"
	if r.ValidateFor(task, now) == nil {
		t.Fatal("allowed status marked mismatched")
	}
	r.ErrorClass = "content_mismatch"
	if r.ValidateFor(task, now) == nil {
		t.Fatal("content mismatch without assertion")
	}
	task.Spec.Assertion = &Assertion{Kind: "text_contains", Text: "ready"}
	if err := r.ValidateFor(task, now); err != nil {
		t.Fatal(err)
	}
}
