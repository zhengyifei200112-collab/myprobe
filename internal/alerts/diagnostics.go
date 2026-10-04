package alerts

import "github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"

type EngineDiagnostics struct {
	Status           string                  `json:"status"`
	Reason           string                  `json:"reason,omitempty"`
	ObservationScope string                  `json:"observation_scope"`
	Evaluation       diagnostics.JobSnapshot `json:"evaluation"`
}

func (s *Service) Diagnostics() EngineDiagnostics {
	result := EngineDiagnostics{Status: "not_running", ObservationScope: "process", Evaluation: s.evaluationJob.Snapshot()}
	if s.cryptoErr != nil {
		result.Status, result.Reason = "disabled", "encryption_configuration_unavailable"
	} else if s.running.Load() {
		result.Status = "running"
	}
	return result
}
