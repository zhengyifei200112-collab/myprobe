package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const PolicyBundleVersion = 1

// PolicyBundle is separate from node configuration and encrypted database backup.
// Source IDs are references for explicit destination mapping, never credentials.
type PolicyBundle struct {
	Format   string           `json:"format"`
	Version  int              `json:"version"`
	Policies []PortablePolicy `json:"policies"`
}

type PortablePolicy struct {
	SourceID        string            `json:"source_id"`
	Name            string            `json:"name"`
	Key             string            `json:"policy_key"`
	Enabled         bool              `json:"enabled"`
	Priority        int               `json:"priority"`
	Scope           alertpolicy.Scope `json:"scope"`
	ChannelID       string            `json:"channel_id"`
	Kind            string            `json:"kind"`
	Config          RuleConfig        `json:"config"`
	CooldownSeconds int               `json:"cooldown_seconds"`
}

func (s *Service) ExportPolicyBundle(ctx context.Context) (PolicyBundle, error) {
	items, err := s.store.ListAlertPolicies(ctx)
	if err != nil {
		return PolicyBundle{}, err
	}
	bundle := PolicyBundle{Format: "myprobe-alert-policies", Version: PolicyBundleVersion, Policies: make([]PortablePolicy, 0, len(items))}
	for _, p := range items {
		// Whitelist config fields, including when exporting imported legacy rules.
		// Reject unknown fields rather than silently dropping unsupported semantics.
		decoder := json.NewDecoder(bytes.NewReader(p.Config))
		decoder.DisallowUnknownFields()
		var config *RuleConfig
		if err := decoder.Decode(&config); err != nil || config == nil {
			return PolicyBundle{}, store.ErrInvalidAlertPolicy
		}
		if !errors.Is(decoder.Decode(new(any)), io.EOF) {
			return PolicyBundle{}, store.ErrInvalidAlertPolicy
		}
		bundle.Policies = append(bundle.Policies, PortablePolicy{SourceID: p.ID, Name: p.Name, Key: p.Key, Enabled: p.Enabled, Priority: p.Priority, Scope: p.Scope, ChannelID: p.ChannelID, Kind: p.Kind, Config: *config, CooldownSeconds: p.CooldownSeconds})
	}
	return bundle, nil
}
