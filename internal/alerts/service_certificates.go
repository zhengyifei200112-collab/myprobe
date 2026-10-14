package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/servicehealth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func (s *Service) prepareCertificates(ctx context.Context, at time.Time) (map[string]*store.CertificatePool, error) {
	pools := map[string]*store.CertificatePool{}
	if !s.httpAlertsEnabled.Load() {
		return pools, nil
	}
	targets, err := s.store.ListCertificateAlertTargets(ctx)
	if err != nil {
		return nil, err
	}
	for _, target := range targets {
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		snapshots, err := s.store.HTTPHealthSnapshots(queryCtx, []string{target.ServiceID}, at)
		if err != nil {
			cancel()
			return nil, err
		}
		if len(snapshots) != 1 {
			cancel()
			continue
		}
		available := map[string]bool{}
		callback := s.httpAvailability.Load()
		for _, o := range snapshots[0].Observers {
			available[o.NodeID] = callback == nil || callback.(func(string) bool)(o.NodeID)
		}
		pool, err := s.store.PrepareCertificateReminders(queryCtx, target, available, at)
		cancel()
		if errors.Is(err, store.ErrObservationObsolete) {
			continue
		}
		if err != nil {
			return nil, err
		}
		pools[target.ServiceID] = pool
	}
	return pools, nil
}

func evaluateCertificate(rule store.AlertRule, pools map[string]*store.CertificatePool, at time.Time) (bool, string, bool, string, error) {
	var cfg store.ServiceCertificateRuleConfig
	if err := json.Unmarshal(rule.Config, &cfg); err != nil {
		return false, "", false, "", err
	}
	if !cfg.NotAfter.After(at) {
		return false, "证书已达到到期时间。", true, "certificate_expired", nil
	}
	pool := pools[cfg.ServiceID]
	if pool == nil || pool.ServiceRevision != cfg.ServiceRevision || pool.AlertRevision != cfg.AlertRevision {
		return false, "", false, "", nil
	}
	candidate, exists := pool.Certificates[cfg.CertificateSHA256]
	if !exists {
		if pool.Complete {
			return false, "完整的新鲜成功检查未再观测到该证书。", true, "certificate_no_longer_observed", nil
		}
		return false, "", false, "", nil
	}
	if !candidate.Certificate.NotBefore.Equal(cfg.NotBefore) || !candidate.Certificate.NotAfter.Equal(cfg.NotAfter) {
		return false, "", false, "", nil
	}
	due := servicehealth.CertificateDueDays(cfg.NotAfter, at)
	if due > 0 && due < cfg.DaysBefore {
		return false, "证书已进入更紧急提醒档。", true, "certificate_threshold_advanced", nil
	}
	if due != cfg.DaysBefore {
		return false, "", false, "", nil
	}
	prefix := cfg.CertificateSHA256
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	message := fmt.Sprintf("服务 %s 的已验证证书将在 %s 到期，已进入 %d 天提醒档（指纹 %s）。请检查证书续期。", cfg.ServiceName, cfg.NotAfter.UTC().Format(time.RFC3339), cfg.DaysBefore, prefix)
	return true, message, true, "", nil
}
