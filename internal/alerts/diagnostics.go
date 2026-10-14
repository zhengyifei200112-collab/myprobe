package alerts

import "github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"

type EngineDiagnostics struct {
	Status                string                  `json:"status"`
	Reason                string                  `json:"reason,omitempty"`
	ObservationScope      string                  `json:"observation_scope"`
	DeliveryConfiguration string                  `json:"delivery_configuration"`
	Evaluation            diagnostics.JobSnapshot `json:"evaluation"`
}

func (s *Service) Diagnostics() EngineDiagnostics {
	result := EngineDiagnostics{Status: "not_running", DeliveryConfiguration: "available", ObservationScope: "process", Evaluation: s.evaluationJob.Snapshot()}
	if s.cryptoErr != nil {
		result.DeliveryConfiguration, result.Reason = "unavailable", "encryption_configuration_unavailable"
	}
	if s.linkErr != nil {
		result.DeliveryConfiguration, result.Reason = "unavailable", "notification_origin_configuration_unavailable"
	}
	if s.running.Load() {
		result.Status = "running"
	}
	return result
}
