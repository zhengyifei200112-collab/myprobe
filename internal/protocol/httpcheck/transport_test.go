package httpcheck

import (
	"testing"
	"time"
)

func TestTransportFailureResultContract(t *testing.T) {
	now := time.Now().UTC()
	task := Task{ID: "task", ServiceID: "service", Revision: 1, ScheduledAt: now, ExpiresAt: now.Add(time.Minute), Spec: Spec{URL: "http://example.test", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}}
	result := Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: 1, ScheduledAt: now, CompletedAt: now, Outcome: "failure", ErrorClass: "transport"}
	for _, status := range []int{0, 200} {
		result.StatusCode = status
		if err := result.ValidateFor(task, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, outcome := range []string{"success", "unobserved"} {
		result.Outcome = outcome
		result.StatusCode = 0
		if result.ValidateFor(task, now) == nil {
			t.Fatalf("transport error accepted as %s", outcome)
		}
	}
}
