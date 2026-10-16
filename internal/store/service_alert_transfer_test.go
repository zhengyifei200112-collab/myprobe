package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func serviceTransferFixture(t *testing.T) (*Store, ServiceAlertBundle, ServiceAlertImportMapping, []HTTPService, NotificationChannel) {
	t.Helper()
	ctx := context.Background()
	source, v, _, node, settings := serviceAlertFixture(t)
	settings.Enabled = false
	settings.CertificateRemindersEnabled = true
	settings.DurationSeconds = 60
	settings.RecoverySeconds = 90
	settings.RepeatSeconds = 600
	if _, err := source.SaveServiceAlertSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	second, err := source.SaveHTTPService(ctx, HTTPService{Name: "second source", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: v.Spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.SaveServiceAlertSettings(ctx, ServiceAlertSettings{ServiceID: second.ID, ServiceRevision: second.Revision, ChannelID: settings.ChannelID, Enabled: true, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	b, err := source.ExportServiceAlerts(ctx)
	if err != nil || len(b.Alerts) != 2 || len(b.Channels) != 1 {
		t.Fatal("source export")
	}
	dest, err := Open(ctx, filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dest.Close() })
	channel, err := dest.CreateNotificationChannel(ctx, "destination telegram", "telegram", "credential-marker")
	if err != nil {
		t.Fatal(err)
	}
	channel, err = dest.UpdateNotificationChannel(ctx, channel.ID, channel.Name, channel.Kind, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	mapping := ServiceAlertImportMapping{Services: map[string]string{}, Channels: map[string]string{b.Channels[0].ID: channel.ID}}
	destNode, _, err := dest.CreateNode(ctx, CreateNodeParams{Name: "destination observer"})
	if err != nil {
		t.Fatal(err)
	}
	services := []HTTPService{}
	for i, source := range b.Alerts {
		v, err := dest.SaveHTTPService(ctx, HTTPService{Name: source.Name + " destination", Enabled: i == 0, IntervalSeconds: 30, NodeIDs: []string{destNode.ID}, Spec: httpcheck.Spec{URL: "http://example.test/private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
		if err != nil {
			t.Fatal(err)
		}
		mapping.Services[source.SourceID] = v.ID
		services = append(services, v)
	}
	if _, err := dest.SaveServiceAlertSettings(ctx, ServiceAlertSettings{ServiceID: services[0].ID, ServiceRevision: services[0].Revision, ChannelID: channel.ID, Enabled: true, CooldownSeconds: 120}); err != nil {
		t.Fatal(err)
	}
	return dest, b, mapping, services, channel
}

func TestServiceAlertTransferCrossDatabasePreviewReplayAndRestore(t *testing.T) {
	s, b, mapping, services, channel := serviceTransferFixture(t)
	ctx := context.Background()
	body, _ := json.Marshal(b)
	for _, private := range []string{"credential-marker", "private-marker", "spec", "config_encrypted", "token", "task", "incident", "certificate_sha256"} {
		if strings.Contains(string(body), private) {
			t.Fatal("private bundle data")
		}
	}
	catalog, err := s.ServiceAlertDestinations(ctx)
	if err != nil || len(catalog.Services) != 2 || len(catalog.Channels) != 1 {
		t.Fatal("destinations")
	}
	before, _ := s.ServiceAlertSettings(ctx, services[0].ID)
	p, err := s.PreviewServiceAlertImport(ctx, b, mapping)
	if err != nil || p.Created != 1 || p.Updated != 1 || len(p.Items) != 2 || p.Items[0].ChannelEnabled || p.Items[0].ChannelProvider != "telegram" {
		t.Fatal("preview")
	}
	unchanged, _ := s.ServiceAlertSettings(ctx, services[0].ID)
	if !reflect.DeepEqual(before, unchanged) || rowCount(t, s, "service_alert_imports") != 0 || rowCount(t, s, "alert_incidents") != 0 {
		t.Fatal("preview wrote state")
	}
	first, err := s.ApplyServiceAlertImport(ctx, "portable-request", b, mapping, p.Digest)
	if err != nil || first.Replayed || first.Created != 1 || first.Updated != 1 {
		t.Fatal("apply")
	}
	for i, v := range b.Alerts {
		current, err := s.ServiceAlertSettings(ctx, services[i].ID)
		if err != nil || current.Enabled != v.Settings.Enabled || current.CertificateRemindersEnabled != v.Settings.CertificateRemindersEnabled || current.DurationSeconds != v.Settings.DurationSeconds || current.ChannelID != mapping.Channels[v.ChannelID] {
			t.Fatal("imported settings")
		}
	}
	if _, err := s.ApplyServiceAlertImport(ctx, "other-request", b, mapping, p.Digest); !errors.Is(err, ErrServiceAlertImportConflict) {
		t.Fatal("stale preview accepted")
	}
	changed := b
	changed.Alerts = append([]PortableServiceAlert(nil), b.Alerts...)
	changed.Alerts[0].Settings.DurationSeconds++
	if _, err := s.ApplyServiceAlertImport(ctx, "portable-request", changed, mapping, p.Digest); !errors.Is(err, ErrServiceAlertImportRequestConflict) {
		t.Fatal("same-key different content")
	}
	if err := s.DeleteHTTPService(ctx, services[0].ID, services[0].Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHTTPService(ctx, services[1].ID, services[1].Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNotificationChannel(ctx, channel.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := s.ApplyServiceAlertImport(ctx, "portable-request", b, mapping, p.Digest)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.ServiceIDs, first.ServiceIDs) || rowCount(t, s, "http_service_alerts") != 0 {
		t.Fatal("replay after deletion")
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	restored, err := copy.ApplyServiceAlertImport(ctx, "portable-request", b, mapping, p.Digest)
	if err != nil || !reflect.DeepEqual(restored, replay) {
		t.Fatal("full backup lost replay identity")
	}
}

func TestServiceAlertTransferRejectsMutableDestinationsAndRollsBack(t *testing.T) {
	for _, kind := range []string{"service", "alert", "channel_credentials", "channel_enabled", "service_deleted", "channel_deleted", "write_failure"} {
		t.Run(kind, func(t *testing.T) {
			s, b, mapping, services, channel := serviceTransferFixture(t)
			ctx := context.Background()
			p, err := s.PreviewServiceAlertImport(ctx, b, mapping)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "service":
				v := services[0]
				v.Name = "edited"
				if _, err := s.SaveHTTPService(ctx, v); err != nil {
					t.Fatal(err)
				}
			case "alert":
				v, _ := s.ServiceAlertSettings(ctx, services[0].ID)
				v.CooldownSeconds++
				if _, err := s.SaveServiceAlertSettings(ctx, v); err != nil {
					t.Fatal(err)
				}
			case "channel_credentials":
				encrypted := "new-credential-marker"
				if _, err := s.UpdateNotificationChannel(ctx, channel.ID, channel.Name, channel.Kind, &encrypted, channel.Enabled); err != nil {
					t.Fatal(err)
				}
			case "channel_enabled":
				if _, err := s.UpdateNotificationChannel(ctx, channel.ID, channel.Name, channel.Kind, nil, true); err != nil {
					t.Fatal(err)
				}
			case "service_deleted":
				if err := s.DeleteHTTPService(ctx, services[1].ID, services[1].Revision); err != nil {
					t.Fatal(err)
				}
			case "channel_deleted":
				extra, err := s.CreateNotificationChannel(ctx, "temporary", "webhook", "fixture")
				if err != nil {
					t.Fatal(err)
				}
				mapping.Channels[b.Channels[0].ID] = extra.ID
				p, err = s.PreviewServiceAlertImport(ctx, b, mapping)
				if err != nil {
					t.Fatal(err)
				}
				if err := s.DeleteNotificationChannel(ctx, extra.ID); err != nil {
					t.Fatal(err)
				}
			case "write_failure":
				if _, err := s.db.Exec(`CREATE TRIGGER reject_import BEFORE INSERT ON service_alert_imports BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := s.ServiceAlertSettings(ctx, services[0].ID)
			if _, err := s.ApplyServiceAlertImport(ctx, "must-not-commit", b, mapping, p.Digest); err == nil {
				t.Fatal("invalidated import accepted")
			}
			after, _ := s.ServiceAlertSettings(ctx, services[0].ID)
			if !reflect.DeepEqual(before, after) || rowCount(t, s, "service_alert_imports") != 0 {
				t.Fatal("partial commit")
			}
		})
	}
}

func TestServiceAlertTransferStrictMappingsParametersAndConcurrentRequests(t *testing.T) {
	s, b, mapping, _, _ := serviceTransferFixture(t)
	ctx := context.Background()
	for _, kind := range []string{"missing_service", "missing_channel", "extra_service", "extra_channel", "collapsed_services", "unknown_service", "unknown_channel", "unknown_version", "invalid_parameter", "unused_ref"} {
		candidate := b
		candidate.Alerts = append([]PortableServiceAlert(nil), b.Alerts...)
		candidate.Channels = append([]ServiceAlertChannelReference(nil), b.Channels...)
		m := ServiceAlertImportMapping{Services: map[string]string{}, Channels: map[string]string{}}
		for k, v := range mapping.Services {
			m.Services[k] = v
		}
		for k, v := range mapping.Channels {
			m.Channels[k] = v
		}
		switch kind {
		case "missing_service":
			delete(m.Services, b.Alerts[0].SourceID)
		case "missing_channel":
			delete(m.Channels, b.Channels[0].ID)
		case "extra_service":
			m.Services["extra"] = "service"
		case "extra_channel":
			m.Channels["extra"] = "channel"
		case "collapsed_services":
			m.Services[b.Alerts[1].SourceID] = m.Services[b.Alerts[0].SourceID]
		case "unknown_service":
			m.Services[b.Alerts[0].SourceID] = "missing"
		case "unknown_channel":
			m.Channels[b.Channels[0].ID] = "missing"
		case "unknown_version":
			candidate.Version = 2
		case "invalid_parameter":
			candidate.Alerts[0].Settings.RepeatSeconds = 1
		case "unused_ref":
			candidate.Channels = append(candidate.Channels, ServiceAlertChannelReference{ID: "unused", Name: "unused", Provider: "smtp"})
			m.Channels["unused"] = "channel"
		}
		if _, err := s.PreviewServiceAlertImport(ctx, candidate, m); !errors.Is(err, ErrServiceAlertImportInvalid) {
			t.Fatal(kind)
		}
	}
	p, err := s.PreviewServiceAlertImport(ctx, b, mapping)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan ServiceAlertImportResult, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.ApplyServiceAlertImport(ctx, "concurrent", b, mapping, p.Digest)
			results <- r
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	replayed := 0
	for r := range results {
		if r.Replayed {
			replayed++
		}
	}
	if replayed != 1 || rowCount(t, s, "service_alert_imports") != 1 {
		t.Fatal("concurrent replay")
	}
	// Existing v1/v2 definition imports remain unchanged and never import settings.
	source, v, _, _, _ := serviceAlertFixture(t)
	config, err := source.ExportConfig(ctx, time.Now())
	if err != nil || config.Version != ConfigSnapshotVersion {
		t.Fatal("current config export")
	}
	for _, version := range []int{1, 2} {
		candidate := config
		candidate.Version = version
		if version == 1 {
			candidate.HTTPServices = nil
		}
		if _, err := s.ImportConfig(ctx, candidate, true); err != nil {
			t.Fatal("legacy dry-run compatibility")
		}
	}
	if _, err := source.ServiceAlertSettings(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
}

func TestServiceAlertTransferCapacityAndConcurrentStalePreview(t *testing.T) {
	s, b, m, _, _ := serviceTransferFixture(t)
	ctx := context.Background()
	p, err := s.PreviewServiceAlertImport(ctx, b, m)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := s.ApplyServiceAlertImport(ctx, key, b, m, p.Digest)
			errs <- err
		}(key)
	}
	wg.Wait()
	close(errs)
	succeeded, conflicted := 0, 0
	for err := range errs {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrServiceAlertImportConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 || rowCount(t, s, "service_alert_imports") != 1 {
		t.Fatal("concurrent stale preview overwrote state")
	}
	var existing string
	if err := s.db.QueryRow(`SELECT request_id FROM service_alert_imports`).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<9999) INSERT INTO service_alert_imports(request_id,request_digest,result_json,created_at) SELECT 'fixture-'||x,hex(zeroblob(32)),'{}','2026-01-01T00:00:00Z' FROM n`); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.PreviewServiceAlertImport(ctx, b, m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyServiceAlertImport(ctx, "over-capacity", b, m, fresh.Digest); !errors.Is(err, ErrServiceAlertImportLimit) {
		t.Fatal("capacity silently accepted")
	}
	if r, err := s.ApplyServiceAlertImport(ctx, existing, b, m, p.Digest); err != nil || !r.Replayed {
		t.Fatal("capacity blocked existing replay")
	}
	empty, err := Open(ctx, filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	blank, err := empty.ExportServiceAlerts(ctx)
	if err != nil || len(blank.Alerts) != 0 || len(blank.Channels) != 0 {
		t.Fatal("empty export")
	}
	if _, err := empty.PreviewServiceAlertImport(ctx, blank, ServiceAlertImportMapping{}); !errors.Is(err, ErrServiceAlertImportInvalid) {
		t.Fatal("empty import")
	}
}

func TestServiceAlertTransferPreviewUsesOneReadSnapshot(t *testing.T) {
	s, b, m, _, channel := serviceTransferFixture(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var seen int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateNotificationChannel(ctx, channel.ID, "renamed destination", channel.Kind, nil, true); err != nil {
		t.Fatal(err)
	}
	old, err := prepareServiceAlertImport(ctx, tx, b, m)
	if err != nil || old.Items[0].ChannelName != channel.Name || old.Items[0].ChannelEnabled {
		t.Fatal("mixed preview snapshot")
	}
	fresh, err := s.PreviewServiceAlertImport(ctx, b, m)
	if err != nil || fresh.Items[0].ChannelName != "renamed destination" || !fresh.Items[0].ChannelEnabled || old.Digest == fresh.Digest {
		t.Fatal("new destination state missing")
	}
}
