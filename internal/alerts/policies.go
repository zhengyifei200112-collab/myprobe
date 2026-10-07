package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

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
