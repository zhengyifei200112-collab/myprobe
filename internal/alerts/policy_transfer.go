package alerts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const PolicyBundleVersion = 1

type PolicyImportMapping struct {
	Channels  map[string]string `json:"channels"`
	Nodes     map[string]string `json:"nodes"`
	Templates map[string]string `json:"templates"`
}

// PolicyImportDigest binds confirmation and retries to the typed source request.
// It is not an authorization token or a reservation of destination state.
func PolicyImportDigest(bundle PolicyBundle, mapping PolicyImportMapping) (string, error) {
	if len(bundle.Policies) == 0 || len(bundle.Policies) > 1000 || len(mapping.Channels) > 1000 || len(mapping.Templates) > 1000 || len(mapping.Nodes) > 100000 {
		return "", store.ErrInvalidAlertPolicy
	}
	raw, err := json.Marshal(struct {
		Bundle  PolicyBundle        `json:"bundle"`
		Mapping PolicyImportMapping `json:"mapping"`
	}{bundle, mapping})
	if err != nil || len(raw) > 16<<20 {
		return "", store.ErrInvalidAlertPolicy
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ApplyPolicyImport hashes the typed request before normalization so retry identity
// does not depend on mutable destination references or changing default values.
func (s *Service) ApplyPolicyImport(ctx context.Context, requestID string, bundle PolicyBundle, mapping PolicyImportMapping) (store.PolicyImportResult, error) {
	digest, err := PolicyImportDigest(bundle, mapping)
	if err != nil {
		return store.PolicyImportResult{}, err
	}
	if replay, err := s.store.LookupPolicyImport(ctx, requestID, digest); err != nil {
		return store.PolicyImportResult{}, err
	} else if replay != nil {
		return *replay, nil
	}
	definitions, err := s.preparePolicyImport(ctx, bundle, mapping)
	if err != nil {
		// Another caller may have committed while this caller validated references.
		if replay, lookupErr := s.store.LookupPolicyImport(ctx, requestID, digest); lookupErr != nil {
			return store.PolicyImportResult{}, lookupErr
		} else if replay != nil {
			return *replay, nil
		}
		return store.PolicyImportResult{}, err
	}
	return s.store.ApplyPolicyImport(ctx, requestID, digest, definitions)
}

// PreviewPolicyImport returns normalized destination definitions without writing.
// SourceID remains a source reference; no temporary Store ID is exposed.
func (s *Service) PreviewPolicyImport(ctx context.Context, bundle PolicyBundle, mapping PolicyImportMapping) (PolicyBundle, error) {
	definitions, err := s.preparePolicyImport(ctx, bundle, mapping)
	if err != nil {
		return PolicyBundle{}, err
	}
	validated, err := s.store.CreateAlertPolicies(ctx, definitions, true)
	if err != nil {
		return PolicyBundle{}, err
	}
	result := PolicyBundle{Format: bundle.Format, Version: bundle.Version, Policies: make([]PortablePolicy, 0, len(validated))}
	for i, p := range validated {
		var config RuleConfig
		if err := json.Unmarshal(p.Config, &config); err != nil {
			return PolicyBundle{}, err
		}
		result.Policies = append(result.Policies, PortablePolicy{SourceID: bundle.Policies[i].SourceID, Name: p.Name, Key: p.Key, Enabled: p.Enabled, Priority: p.Priority, Scope: p.Scope, ChannelID: p.ChannelID, Kind: p.Kind, Config: config, CooldownSeconds: p.CooldownSeconds})
	}
	return result, nil
}

func (s *Service) preparePolicyImport(ctx context.Context, bundle PolicyBundle, mapping PolicyImportMapping) ([]store.AlertPolicy, error) {
	if bundle.Format != "myprobe-alert-policies" || bundle.Version != PolicyBundleVersion || len(bundle.Policies) == 0 || len(bundle.Policies) > 1000 || len(mapping.Channels) > 1000 || len(mapping.Templates) > 1000 || len(mapping.Nodes) > 100000 {
		return nil, store.ErrInvalidAlertPolicy
	}
	seen := make(map[string]bool)
	definitions := make([]store.AlertPolicy, 0, len(bundle.Policies))
	for _, p := range bundle.Policies {
		if p.SourceID == "" || len(p.SourceID) > 128 || strings.TrimSpace(p.SourceID) != p.SourceID || seen[p.SourceID] {
			return nil, store.ErrInvalidAlertPolicy
		}
		seen[p.SourceID] = true
		channelID := mapping.Channels[p.ChannelID]
		if channelID == "" {
			return nil, store.ErrInvalidAlertPolicy
		}
		scope := p.Scope
		scope.NodeIDs = append([]string(nil), p.Scope.NodeIDs...)
		scope.Tags = append([]string(nil), p.Scope.Tags...)
		for i, source := range scope.NodeIDs {
			if mapping.Nodes[source] == "" {
				return nil, store.ErrInvalidAlertPolicy
			}
			scope.NodeIDs[i] = mapping.Nodes[source]
		}
		config := p.Config
		if config.TemplateID != "" {
			config.TemplateID = mapping.Templates[config.TemplateID]
			if config.TemplateID == "" {
				return nil, store.ErrInvalidAlertPolicy
			}
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, store.ErrInvalidAlertPolicy
		}
		definition, err := s.validatePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: p.Key, Enabled: p.Enabled, Priority: p.Priority, Scope: scope}, Name: p.Name, ChannelID: channelID, Kind: p.Kind, Config: raw, CooldownSeconds: p.CooldownSeconds})
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, definition)
	}
	return definitions, nil
}

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
