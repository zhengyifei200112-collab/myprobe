package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/servicehealth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Service) EnableHTTPServiceAlerts(enabled bool) { s.httpAlertsEnabled.Store(enabled) }

func (s *Service) SetHTTPAgentAvailability(available func(string) bool) {
	if available != nil {
		s.httpAvailability.Store(available)
	}
}

func (s *Service) evaluateHTTPService(ctx context.Context, rule store.AlertRule, node store.Node, now time.Time) (bool, string, bool, error) {
	availableNow := true
	if available := s.httpAvailability.Load(); available != nil && !available.(func(string) bool)(node.ID) {
		availableNow = false
	}
	input := servicehealth.Input{ServiceEnabled: true, ExecutionEnabled: s.httpAlertsEnabled.Load(), AgentAvailable: availableNow, LastSeenAt: node.LastSeenAt, ReportSeconds: node.ReportSeconds}
	if node.Agent != nil {
		input.Capabilities = node.Agent.Capabilities
	}
	if servicehealth.Evaluate(input, now).Reason != "not_observed" {
		return false, "", false, nil
	}
	var config store.ServiceRuleConfig
	if err := json.Unmarshal(rule.Config, &config); err != nil {
		return false, "", false, err
	}
	observation, interval, err := s.store.LatestServiceAlertObservation(ctx, config.ServiceID, node.ID, config.ServiceRevision, now)
	if err != nil {
		return false, "", false, err
	}
	input.IntervalSeconds = interval
	if observation != nil {
		input.Observation = &servicehealth.Observation{ScheduledAt: observation.ScheduledAt, ExpiresAt: observation.ExpiresAt, Result: observation.Result}
	}
	assessment := servicehealth.Evaluate(input, now)
	if assessment.Status == "success" {
		return false, fmt.Sprintf("服务 %s 从观测点 %s 的检查已恢复正常。", config.ServiceName, node.Name), true, nil
	}
	if assessment.Status != "failure" {
		return false, "", false, nil
	}
	return true, fmt.Sprintf("服务 %s 从观测点 %s 的检查失败：%s。", config.ServiceName, node.Name, servicehealth.FailureDescription(assessment.ErrorClass)), true, nil
}
