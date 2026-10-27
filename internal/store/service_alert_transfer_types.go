package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrServiceAlertImportInvalid = errors.New("invalid service alert import")
var ErrServiceAlertImportConflict = errors.New("service alert import content or destination changed")
var ErrServiceAlertImportRequestConflict = errors.New("service alert import request ID reused")
var ErrServiceAlertImportLimit = errors.New("service alert import limit reached")

type ServiceAlertParameters struct {
	Enabled                     bool `json:"enabled"`
	CertificateRemindersEnabled bool `json:"certificate_reminders_enabled"`
	DurationSeconds             int  `json:"duration_seconds"`
	RecoverySeconds             int  `json:"recovery_seconds"`
	RepeatSeconds               int  `json:"repeat_seconds"`
	CooldownSeconds             int  `json:"cooldown_seconds"`
}

// Explicit false/zero is meaningful. Missing/null parameters are never defaults.
func (p *ServiceAlertParameters) UnmarshalJSON(raw []byte) error {
	var value struct {
		Enabled     *bool `json:"enabled"`
		Certificate *bool `json:"certificate_reminders_enabled"`
		Duration    *int  `json:"duration_seconds"`
		Recovery    *int  `json:"recovery_seconds"`
		Repeat      *int  `json:"repeat_seconds"`
		Cooldown    *int  `json:"cooldown_seconds"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&value) != nil || !errors.Is(d.Decode(new(any)), io.EOF) || value.Enabled == nil || value.Certificate == nil || value.Duration == nil || value.Recovery == nil || value.Repeat == nil || value.Cooldown == nil {
		return ErrServiceAlertImportInvalid
	}
	*p = ServiceAlertParameters{*value.Enabled, *value.Certificate, *value.Duration, *value.Recovery, *value.Repeat, *value.Cooldown}
	return p.validate()
}

func (p ServiceAlertParameters) settings(serviceID, channelID string, serviceRevision, revision int64) ServiceAlertSettings {
	return ServiceAlertSettings{ServiceID: serviceID, ChannelID: channelID, ServiceRevision: serviceRevision, Revision: revision, Enabled: p.Enabled, CertificateRemindersEnabled: p.CertificateRemindersEnabled, DurationSeconds: p.DurationSeconds, RecoverySeconds: p.RecoverySeconds, RepeatSeconds: p.RepeatSeconds, CooldownSeconds: p.CooldownSeconds}
}
func (p ServiceAlertParameters) validate() error {
	if validateServiceAlertSettings(p.settings("service", "channel", 1, 0)) != nil {
		return ErrServiceAlertImportInvalid
	}
	return nil
}

type PortableServiceAlert struct {
	SourceID  string                 `json:"source_id"`
	Name      string                 `json:"name"`
	ChannelID string                 `json:"channel_id"`
	Settings  ServiceAlertParameters `json:"settings"`
}
type ServiceAlertChannelReference struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}
type ServiceAlertBundle struct {
	Format   string                         `json:"format"`
	Version  int                            `json:"version"`
	Alerts   []PortableServiceAlert         `json:"alerts"`
	Channels []ServiceAlertChannelReference `json:"channels"`
}
type ServiceAlertImportMapping struct {
	Services map[string]string `json:"services"`
	Channels map[string]string `json:"channels"`
}
type ServiceAlertDestination struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider,omitempty"`
}
type ServiceAlertDestinations struct {
	Services []ServiceAlertDestination `json:"services"`
	Channels []ServiceAlertDestination `json:"channels"`
}
type ServiceAlertImportItem struct {
	SourceID                string                  `json:"source_id"`
	ServiceID               string                  `json:"service_id"`
	ServiceName             string                  `json:"service_name"`
	ServiceEnabled          bool                    `json:"service_enabled"`
	ServiceRevision         int64                   `json:"service_revision"`
	ServiceUpdatedAt        string                  `json:"service_updated_at"`
	AlertRevision           int64                   `json:"alert_revision"`
	AlertUpdatedAt          string                  `json:"alert_updated_at"`
	ChannelID               string                  `json:"channel_id"`
	ChannelName             string                  `json:"channel_name"`
	ChannelProvider         string                  `json:"channel_provider"`
	ChannelEnabled          bool                    `json:"channel_enabled"`
	ChannelDeliveryRevision int64                   `json:"channel_delivery_revision"`
	Before                  *ServiceAlertParameters `json:"before"`
	BeforeChannelID         string                  `json:"before_channel_id,omitempty"`
	BeforeChannelName       string                  `json:"before_channel_name,omitempty"`
	BeforeChannelProvider   string                  `json:"before_channel_provider,omitempty"`
	After                   ServiceAlertParameters  `json:"after"`
}
type ServiceAlertImportPreview struct {
	Items   []ServiceAlertImportItem `json:"items"`
	Created int                      `json:"created"`
	Updated int                      `json:"updated"`
	Digest  string                   `json:"preview_digest"`
}
type ServiceAlertImportResult struct {
	ServiceIDs []string `json:"service_ids"`
	Created    int      `json:"created"`
	Updated    int      `json:"updated"`
	Replayed   bool     `json:"replayed"`
}

func validServiceTransferName(name string) bool {
	return utf8.ValidString(name) && len(name) > 0 && len(name) <= 200 && strings.TrimSpace(name) == name && strings.IndexFunc(name, unicode.IsControl) < 0
}
func validateServiceAlertBundle(b ServiceAlertBundle, empty bool) error {
	if b.Format != "myprobe-service-alerts" || b.Version != 1 || (!empty && len(b.Alerts) == 0) || len(b.Alerts) > 1000 || len(b.Channels) > 1000 {
		return ErrServiceAlertImportInvalid
	}
	refs := map[string]bool{}
	for _, c := range b.Channels {
		if !portableID.MatchString(c.ID) || refs[c.ID] || !validServiceTransferName(c.Name) || !validChannelKind(c.Provider) {
			return ErrServiceAlertImportInvalid
		}
		refs[c.ID] = true
	}
	used, seen := map[string]bool{}, map[string]bool{}
	for _, v := range b.Alerts {
		if !portableID.MatchString(v.SourceID) || seen[v.SourceID] || !validServiceTransferName(v.Name) || !refs[v.ChannelID] || v.Settings.validate() != nil {
			return ErrServiceAlertImportInvalid
		}
		seen[v.SourceID], used[v.ChannelID] = true, true
	}
	if len(used) != len(refs) {
		return ErrServiceAlertImportInvalid
	}
	return nil
}
