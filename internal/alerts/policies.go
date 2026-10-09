package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// PolicyEvaluationStatus describes preparation, not observation freshness or
// notification delivery. A saved edit is applied on the next successful tick.
type PolicyEvaluationStatus struct {
	State         string     `json:"state"`
	LastAppliedAt *time.Time `json:"last_applied_at,omitempty"`
}

func (s *Service) PolicyEvaluationStatus() PolicyEvaluationStatus {
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	status := s.policyStatus
	if status.State == "" {
		status.State = "pending"
	}
	if status.LastAppliedAt != nil {
		at := *status.LastAppliedAt
		status.LastAppliedAt = &at
	}
	return status
}

func (s *Service) setPolicyEvaluationStatus(state string, at *time.Time) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	s.policyStatus.State = state
	if at != nil {
		stamp := at.UTC()
		s.policyStatus.LastAppliedAt = &stamp
	}
}

// SavePolicy is the write boundary for scoped policy administration. Reuse rule
// defaults and threshold semantics, while rejecting unknown input fields so a
// misspelled threshold cannot silently become a default.
func (s *Service) SavePolicy(ctx context.Context, policy store.AlertPolicy) (store.AlertPolicy, error) {
	if len(policy.Config) == 0 || len(policy.Config) > 16384 {
		return store.AlertPolicy{}, store.ErrInvalidAlertPolicy
	}
	decoder := json.NewDecoder(bytes.NewReader(policy.Config))
	decoder.DisallowUnknownFields()
	var config *RuleConfig
	if err := decoder.Decode(&config); err != nil || config == nil {
		return store.AlertPolicy{}, store.ErrInvalidAlertPolicy
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return store.AlertPolicy{}, store.ErrInvalidAlertPolicy
	}
	raw, err := normalizeRuleConfig(policy.Kind, *config)
	if err != nil {
		return store.AlertPolicy{}, store.ErrInvalidAlertPolicy
	}
	if config.TemplateID != "" {
		if _, err := s.store.NotificationTemplate(ctx, config.TemplateID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return store.AlertPolicy{}, store.ErrInvalidAlertPolicy
			}
			return store.AlertPolicy{}, err
		}
	}
	policy.Config = raw
	return s.store.SaveAlertPolicy(ctx, policy)
}
