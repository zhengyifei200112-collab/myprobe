package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type RuleConfig struct {
	OfflineSeconds          int     `json:"offline_seconds,omitempty"`
	ThresholdPercent        float64 `json:"threshold_percent,omitempty"`
	ThresholdBytesPerSecond uint64  `json:"threshold_bytes_per_second,omitempty"`
	ThresholdBytes          uint64  `json:"threshold_bytes,omitempty"`
	ThresholdMilliseconds   float64 `json:"threshold_milliseconds,omitempty"`
	DaysBefore              int     `json:"days_before,omitempty"`
	DurationSeconds         int     `json:"duration_seconds,omitempty"`
	RepeatSeconds           int     `json:"repeat_seconds,omitempty"`
	TemplateID              string  `json:"template_id,omitempty"`
}

type Service struct {
	store     *store.Store
	crypto    *cryptoBox
	cryptoErr error
	sender    Sender
	logger    *slog.Logger
	interval  time.Duration
}

func New(database *store.Store, encryptionKey string, sender Sender, logger *slog.Logger) *Service {
	box, err := newCryptoBox(encryptionKey)
	if sender == nil {
		sender = NewHTTPSender(nil)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: database, crypto: box, cryptoErr: err, sender: sender, logger: logger, interval: 15 * time.Second}
}

func (s *Service) CreateChannel(ctx context.Context, name, kind string, config ChannelConfig) (store.NotificationChannel, error) {
	encrypted, err := s.encryptConfig(kind, config)
	if err != nil {
		return store.NotificationChannel{}, err
	}
	return s.store.CreateNotificationChannel(ctx, name, kind, encrypted)
}

func (s *Service) UpdateChannel(ctx context.Context, id, name, kind string, config *ChannelConfig, enabled bool) (store.NotificationChannel, error) {
	var encrypted *string
	if config != nil {
		existing, err := s.store.NotificationChannel(ctx, id)
		if err != nil {
			return store.NotificationChannel{}, err
		}
		if existing.Kind != kind { // changing provider always requires a complete replacement configuration
			if err := validateChannelConfig(kind, *config); err != nil {
				return store.NotificationChannel{}, err
			}
		} else if current, err := s.decryptConfig(existing); err == nil {
			*config = mergeChannelConfig(current, *config)
		}
		value, err := s.encryptConfig(kind, *config)
		if err != nil {
			return store.NotificationChannel{}, err
		}
		encrypted = &value
	} else {
		existing, err := s.store.NotificationChannel(ctx, id)
		if err != nil {
			return store.NotificationChannel{}, err
		}
		if existing.Kind != kind {
			return store.NotificationChannel{}, errors.New("new credentials are required when changing channel type")
		}
	}
	return s.store.UpdateNotificationChannel(ctx, id, name, kind, encrypted, enabled)
}

func mergeChannelConfig(current, next ChannelConfig) ChannelConfig {
	if next.URL != "" {
		current.URL = next.URL
	}
	if next.Method != "" {
		current.Method = next.Method
	}
	if next.Headers != nil {
		current.Headers = next.Headers
	}
	if next.BodyTemplate != "" {
		current.BodyTemplate = next.BodyTemplate
	}
	if next.BotToken != "" {
		current.BotToken = next.BotToken
	}
	if next.ChatID != "" {
		current.ChatID = next.ChatID
	}
	if next.ThreadID != "" {
		current.ThreadID = next.ThreadID
	}
	if next.ParseMode != "" {
		current.ParseMode = next.ParseMode
	}
	if next.SMTPHost != "" {
		current.SMTPHost = next.SMTPHost
	}
	if next.SMTPPort != 0 {
		current.SMTPPort = next.SMTPPort
	}
	if next.SMTPUsername != "" {
		current.SMTPUsername = next.SMTPUsername
	}
	if next.SMTPPassword != "" {
		current.SMTPPassword = next.SMTPPassword
	}
	if next.SMTPFrom != "" {
		current.SMTPFrom = next.SMTPFrom
	}
	if next.SMTPTo != "" {
		current.SMTPTo = next.SMTPTo
	}
	if next.SMTPEncryption != "" {
		current.SMTPEncryption = next.SMTPEncryption
	}
	return current
}

func (s *Service) DeleteChannel(ctx context.Context, id string) error {
	return s.store.DeleteNotificationChannel(ctx, id)
}

func (s *Service) ListChannels(ctx context.Context) ([]store.NotificationChannel, error) {
	return s.store.ListNotificationChannels(ctx)
}

func (s *Service) TestChannel(ctx context.Context, id string, now time.Time) error {
	channel, err := s.store.NotificationChannel(ctx, id)
	if err != nil {
		return err
	}
	config, err := s.decryptConfig(channel)
	if err != nil {
		return err
	}
	err = s.sender.Deliver(ctx, channel.Kind, config, Notification{Title: "MyProbe 通知测试", Message: "通知通道配置有效。", State: "test", Kind: "test", Timestamp: now.UTC()})
	message := ""
	if err != nil {
		message = err.Error()
	}
	_ = s.store.RecordChannelTest(ctx, id, err == nil, message, now.UTC())
	return err
}

func (s *Service) TestTemplate(ctx context.Context, templateID, channelID string, now time.Time) error {
	template, err := s.store.NotificationTemplate(ctx, templateID)
	if err != nil {
		return err
	}
	channel, err := s.store.NotificationChannel(ctx, channelID)
	if err != nil {
		return err
	}
	config, err := s.decryptConfig(channel)
	if err != nil {
		return err
	}
	n := Notification{Title: "MyProbe 模板测试", Message: "这是一条模板预览消息。", State: "test", Kind: "test", NodeID: "example-node", NodeName: "Example Node", RuleID: "example-rule", Timestamp: now.UTC()}
	n.Title = renderTemplate(template.TitleTemplate, n)
	n.Message = renderTemplate(template.BodyTemplate, n)
	err = s.sender.Deliver(ctx, channel.Kind, config, n)
	message := ""
	if err != nil {
		message = err.Error()
	}
	_ = s.store.RecordChannelTest(ctx, channelID, err == nil, message, now.UTC())
	return err
}

func (s *Service) CreateRule(ctx context.Context, nodeID, channelID, kind string, config RuleConfig, cooldown int) (store.AlertRule, error) {
	raw, err := normalizeRuleConfig(kind, config)
	if err != nil {
		return store.AlertRule{}, err
	}
	return s.store.CreateAlertRule(ctx, nodeID, channelID, kind, raw, cooldown)
}

func (s *Service) UpdateRule(ctx context.Context, id, nodeID, channelID, kind string, config RuleConfig, enabled bool, cooldown int) (store.AlertRule, error) {
	raw, err := normalizeRuleConfig(kind, config)
	if err != nil {
		return store.AlertRule{}, err
	}
	return s.store.UpdateAlertRule(ctx, id, nodeID, channelID, kind, raw, enabled, cooldown)
}

func (s *Service) Run(ctx context.Context) {
	if s.cryptoErr != nil {
		s.logger.Warn("notification engine disabled", "error", s.cryptoErr)
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if err := s.Tick(ctx, time.Now().UTC()); err != nil && !errors.Is(err, context.Canceled) {
			s.logger.Error("evaluate alert rules", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) Tick(ctx context.Context, now time.Time) error {
	if s.cryptoErr != nil {
		return s.cryptoErr
	}
	rules, err := s.store.ListAlertRules(ctx)
	if err != nil {
		return err
	}
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return err
	}
	channels, err := s.store.ListNotificationChannels(ctx)
	if err != nil {
		return err
	}
	nodeMap := make(map[string]store.Node, len(nodes))
	for _, item := range nodes {
		nodeMap[item.ID] = item
	}
	channelMap := make(map[string]store.NotificationChannel, len(channels))
	for _, item := range channels {
		channelMap[item.ID] = item
	}
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		node, nodeOK := nodeMap[rule.NodeID]
		channel, channelOK := channelMap[rule.ChannelID]
		if !nodeOK || !channelOK || !channel.Enabled {
			continue
		}
		if err := s.evaluateAndDeliver(ctx, rule, node, channel, now.UTC()); err != nil {
			s.logger.Warn("alert evaluation failed", "rule_id", rule.ID, "error", err)
		}
	}
	return nil
}

func (s *Service) evaluateAndDeliver(ctx context.Context, rule store.AlertRule, node store.Node, channel store.NotificationChannel, now time.Time) error {
	active, message, known, err := s.evaluate(ctx, rule, node, now)
	if err != nil || !known {
		return err
	}
	fingerprint := rule.Kind + ":" + rule.ID + ":" + node.ID
	state, exists, err := s.store.AlertState(ctx, fingerprint)
	if err != nil {
		return err
	}
	var ruleConfig RuleConfig
	_ = json.Unmarshal(rule.Config, &ruleConfig)
	if active && ruleConfig.DurationSeconds > 0 && (!exists || !state.Active) {
		if !exists || state.PendingSince == nil {
			since := now
			return s.store.SetAlertPending(ctx, rule.ID, node.ID, fingerprint, message, &since, now)
		}
		if now.Sub(*state.PendingSince) < time.Duration(ruleConfig.DurationSeconds)*time.Second {
			return nil
		}
	}
	if !active && exists && !state.Active && state.PendingSince != nil {
		return s.store.SetAlertPending(ctx, rule.ID, node.ID, fingerprint, message, nil, now)
	}
	repeat := rule.CooldownSeconds
	if ruleConfig.RepeatSeconds > 0 {
		repeat = ruleConfig.RepeatSeconds
	}
	cooldown := time.Duration(repeat) * time.Second
	shouldDeliver := !exists && active
	if exists {
		shouldDeliver = active != state.Active
		if !shouldDeliver && state.LastError != "" && now.Sub(state.LastAttemptAt) >= cooldown {
			shouldDeliver = true
		}
		if !shouldDeliver && active && state.LastError == "" && state.LastDeliveredAt != nil && now.Sub(*state.LastDeliveredAt) >= cooldown {
			shouldDeliver = true
		}
	}
	if !shouldDeliver {
		return nil
	}
	config, deliveryErr := s.decryptConfig(channel)
	if deliveryErr == nil {
		stateName := "resolved"
		title := "MyProbe 告警恢复"
		if active {
			stateName = "firing"
			title = "MyProbe 告警"
		}
		notification := Notification{Title: title, Message: message, State: stateName, Kind: rule.Kind, NodeID: node.ID, NodeName: node.Name, RuleID: rule.ID, Timestamp: now}
		if ruleConfig.TemplateID != "" {
			if template, err := s.store.NotificationTemplate(ctx, ruleConfig.TemplateID); err == nil {
				notification.Title = renderTemplate(template.TitleTemplate, notification)
				notification.Message = renderTemplate(template.BodyTemplate, notification)
			}
		}
		deliveryErr = s.sender.Deliver(ctx, channel.Kind, config, notification)
	}
	deliveryError := ""
	if deliveryErr != nil {
		deliveryError = deliveryErr.Error()
	}
	if err := s.store.RecordAlertAttempt(ctx, rule.ID, node.ID, fingerprint, message, active, deliveryErr == nil, deliveryError, now); err != nil {
		return err
	}
	return deliveryErr
}

func (s *Service) evaluate(ctx context.Context, rule store.AlertRule, node store.Node, now time.Time) (bool, string, bool, error) {
	var config RuleConfig
	if err := json.Unmarshal(rule.Config, &config); err != nil {
		return false, "", false, err
	}
	switch rule.Kind {
	case "offline":
		lastSeen := node.CreatedAt
		if node.LastSeenAt != nil {
			lastSeen = *node.LastSeenAt
		}
		active := now.Sub(lastSeen) >= time.Duration(config.OfflineSeconds)*time.Second
		if active {
			return true, fmt.Sprintf("节点 %s 已离线，最后在线时间 %s。", node.Name, lastSeen.Local().Format("2006-01-02 15:04:05")), true, nil
		}
		return false, fmt.Sprintf("节点 %s 已恢复在线。", node.Name), true, nil
	case "cpu", "memory", "disk", "bandwidth", "cycle_traffic":
		report, err := s.store.LatestReport(ctx, node.ID)
		if err != nil || report == nil {
			return false, "", false, err
		}
		if rule.Kind == "cpu" {
			active := report.CPU.UsagePercent >= config.ThresholdPercent
			return active, thresholdMessage(node.Name, "CPU", report.CPU.UsagePercent, config.ThresholdPercent, "%", active), true, nil
		}
		if rule.Kind == "memory" {
			active := report.Memory.UsagePercent >= config.ThresholdPercent
			return active, thresholdMessage(node.Name, "内存", report.Memory.UsagePercent, config.ThresholdPercent, "%", active), true, nil
		}
		if rule.Kind == "disk" {
			value := 0.0
			for _, disk := range report.Disks {
				if disk.UsagePercent > value {
					value = disk.UsagePercent
				}
			}
			active := value >= config.ThresholdPercent
			return active, thresholdMessage(node.Name, "磁盘", value, config.ThresholdPercent, "%", active), true, nil
		}
		if rule.Kind == "bandwidth" {
			var rate float64
			for _, network := range report.Networks {
				rate += network.RXBytesPerS + network.TXBytesPerS
			}
			active := rate >= float64(config.ThresholdBytesPerSecond)
			return active, thresholdMessage(node.Name, "总带宽", rate, float64(config.ThresholdBytesPerSecond), " B/s", active), true, nil
		}
		traffic, err := s.store.TrafficUsage(ctx, node.ID, node.TrafficResetDay, now)
		if err != nil {
			return false, "", false, err
		}
		used := traffic.RXBytes + traffic.TXBytes
		active := used >= config.ThresholdBytes
		return active, thresholdMessage(node.Name, "周期流量", float64(used), float64(config.ThresholdBytes), " B", active), true, nil
	case "expiry":
		if node.ExpiresAt == nil {
			return false, fmt.Sprintf("节点 %s 已取消到期时间。", node.Name), true, nil
		}
		active := !node.ExpiresAt.After(now.Add(time.Duration(config.DaysBefore) * 24 * time.Hour))
		if active {
			return true, fmt.Sprintf("节点 %s 将于 %s 到期。", node.Name, node.ExpiresAt.Local().Format("2006-01-02 15:04:05")), true, nil
		}
		return false, fmt.Sprintf("节点 %s 的到期时间已恢复到安全范围。", node.Name), true, nil
	case "latency":
		items, err := s.store.ListLatestLatency(ctx, node.ID)
		if err != nil || len(items) == 0 {
			return false, "", false, err
		}
		active, worst := false, 0.0
		for _, item := range items {
			if item.Success != nil && !*item.Success {
				active = true
			}
			if item.LatencyMS != nil && *item.LatencyMS > worst {
				worst = *item.LatencyMS
			}
		}
		if worst >= config.ThresholdMilliseconds {
			active = true
		}
		return active, thresholdMessage(node.Name, "网络延迟", worst, config.ThresholdMilliseconds, " ms", active), true, nil
	default:
		return false, "", false, errors.New("unsupported alert rule")
	}
}

func (s *Service) encryptConfig(kind string, config ChannelConfig) (string, error) {
	if s.cryptoErr != nil {
		return "", s.cryptoErr
	}
	if err := validateChannelConfig(kind, config); err != nil {
		return "", err
	}
	raw, _ := json.Marshal(config)
	return s.crypto.seal(raw)
}

func (s *Service) decryptConfig(channel store.NotificationChannel) (ChannelConfig, error) {
	if s.cryptoErr != nil {
		return ChannelConfig{}, s.cryptoErr
	}
	raw, err := s.crypto.open(channel.ConfigEncrypted)
	if err != nil {
		return ChannelConfig{}, err
	}
	var config ChannelConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return ChannelConfig{}, err
	}
	return config, validateChannelConfig(channel.Kind, config)
}

func normalizeRuleConfig(kind string, config RuleConfig) (json.RawMessage, error) {
	if config.DurationSeconds < 0 || config.DurationSeconds > 86400*30 || config.RepeatSeconds < 0 || config.RepeatSeconds > 86400*30 {
		return nil, errors.New("invalid duration or repeat interval")
	}
	switch kind {
	case "offline":
		if config.OfflineSeconds == 0 {
			config.OfflineSeconds = 60
		}
		if config.OfflineSeconds < 15 || config.OfflineSeconds > 86400 {
			return nil, errors.New("offline threshold must be between 15 and 86400 seconds")
		}
	case "cpu", "memory", "disk":
		if config.ThresholdPercent == 0 {
			config.ThresholdPercent = 90
		}
		if math.IsNaN(config.ThresholdPercent) || config.ThresholdPercent < 1 || config.ThresholdPercent > 100 {
			return nil, errors.New("CPU threshold must be between 1 and 100 percent")
		}
	case "bandwidth":
		if config.ThresholdBytesPerSecond == 0 {
			return nil, errors.New("bandwidth threshold is required")
		}
	case "cycle_traffic":
		if config.ThresholdBytes == 0 {
			return nil, errors.New("cycle traffic threshold is required")
		}
	case "expiry":
		if config.DaysBefore < 0 || config.DaysBefore > 3650 {
			return nil, errors.New("expiry days must be between 0 and 3650")
		}
	case "latency":
		if config.ThresholdMilliseconds <= 0 || config.ThresholdMilliseconds > 60000 {
			return nil, errors.New("latency threshold must be between 0 and 60000 milliseconds")
		}
	default:
		return nil, errors.New("unsupported alert rule")
	}
	return json.Marshal(config)
}

func thresholdMessage(nodeName, metric string, value, threshold float64, unit string, active bool) string {
	if active {
		return fmt.Sprintf("节点 %s 的%s达到 %.2f%s（阈值 %.2f%s）。", nodeName, metric, value, unit, threshold, unit)
	}
	return fmt.Sprintf("节点 %s 的%s已恢复至 %.2f%s（阈值 %.2f%s）。", nodeName, metric, value, unit, threshold, unit)
}

func PublicRuleConfig(rule store.AlertRule) RuleConfig {
	var config RuleConfig
	_ = json.Unmarshal(rule.Config, &config)
	return config
}

func RedactedChannel(channel store.NotificationChannel) store.NotificationChannel {
	channel.ConfigEncrypted = ""
	return channel
}

func validateName(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("name is required")
	}
	return nil
}
