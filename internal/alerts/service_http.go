package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Service) EnableHTTPServiceAlerts(enabled bool) { s.httpAlertsEnabled.Store(enabled) }

func (s *Service) SetHTTPAgentAvailability(available func(string) bool) {
	if available != nil {
		s.httpAvailability.Store(available)
	}
}

func (s *Service) evaluateHTTPService(ctx context.Context, rule store.AlertRule, node store.Node, now time.Time) (bool, string, bool, error) {
	if !s.httpAlertsEnabled.Load() || node.LastSeenAt == nil || node.LastSeenAt.After(now.Add(30*time.Second)) || now.Sub(*node.LastSeenAt) > time.Duration(max(node.ReportSeconds*3, 20))*time.Second {
		return false, "", false, nil
	}
	if node.Agent == nil || !v2.AllowsHTTP(node.Agent.Capabilities) {
		return false, "", false, nil
	}
	if available := s.httpAvailability.Load(); available != nil && !available.(func(string) bool)(node.ID) {
		return false, "", false, nil
	}
	var config store.ServiceRuleConfig
	if err := json.Unmarshal(rule.Config, &config); err != nil {
		return false, "", false, err
	}
	observation, interval, err := s.store.LatestServiceAlertObservation(ctx, config.ServiceID, node.ID, config.ServiceRevision, now)
	if err != nil || observation == nil || observation.Result == nil {
		return false, "", false, err
	}
	result := observation.Result
	if !observationFresh(observation.ScheduledAt, now, interval) || !observationFresh(result.CompletedAt, now, interval) || result.Outcome == "unobserved" {
		return false, "", false, nil
	}
	if result.Outcome == "success" {
		return false, fmt.Sprintf("服务 %s 从观测点 %s 的检查已恢复正常。", config.ServiceName, node.Name), true, nil
	}
	reasons := map[string]string{"dns": "DNS 解析失败", "refused": "连接被拒绝", "timeout": "请求超时", "tls_invalid": "TLS 验证失败", "certificate_expired": "证书已过期", "status_mismatch": "状态码不符合要求", "content_mismatch": "内容断言不符合要求", "response_too_large": "响应超过大小限制", "redirect_limit": "重定向超过限制"}
	reason, valid := reasons[result.ErrorClass]
	if result.Outcome != "failure" || !valid {
		return false, "", false, nil
	}
	return true, fmt.Sprintf("服务 %s 从观测点 %s 的检查失败：%s。", config.ServiceName, node.Name, reason), true, nil
}
