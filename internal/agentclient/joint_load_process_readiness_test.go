package agentclient

import (
	"context"
	"net/http"
	"time"
)

type jointReadiness struct {
	class   string
	elapsed time.Duration
	exited  bool
	err     error
}

func waitJointReadiness(ctx context.Context, client *http.Client, base string, done <-chan error, limit time.Duration) (result jointReadiness) {
	started := time.Now()
	defer func() { result.elapsed = time.Since(started) }()
	bounded, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	ended := func() jointReadiness {
		if ctx.Err() != nil {
			return jointReadiness{class: "parent_context_ended"}
		}
		return jointReadiness{class: "readiness_deadline"}
	}
	for {
		select {
		case err := <-done:
			return jointReadiness{class: "process_exited", exited: true, err: err}
		case <-bounded.Done():
			return ended()
		default:
		}
		request, err := http.NewRequestWithContext(bounded, http.MethodGet, base+"/healthz", nil)
		if err != nil {
			return jointReadiness{class: "invalid_health_endpoint"}
		}
		response, err := client.Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				select {
				case err := <-done:
					return jointReadiness{class: "process_exited", exited: true, err: err}
				default:
					return jointReadiness{class: "ready"}
				}
			}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case err := <-done:
			timer.Stop()
			return jointReadiness{class: "process_exited", exited: true, err: err}
		case <-bounded.Done():
			timer.Stop()
			return ended()
		case <-timer.C:
		}
	}
}
