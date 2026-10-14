package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

func (s *Store) ExportServiceAlerts(ctx context.Context) (ServiceAlertBundle, error) {
	b := ServiceAlertBundle{Format: "myprobe-service-alerts", Version: 1, Alerts: []PortableServiceAlert{}, Channels: []ServiceAlertChannelReference{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return b, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.service_id,s.name,a.channel_id,c.name,COALESCE(NULLIF(c.provider,''),c.kind),a.enabled,a.certificate_reminders_enabled,a.duration_seconds,a.recovery_seconds,a.repeat_seconds,a.cooldown_seconds FROM http_service_alerts a LEFT JOIN http_services s ON s.id=a.service_id LEFT JOIN notification_channels c ON c.id=a.channel_id ORDER BY a.service_id LIMIT 1001`)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		if len(b.Alerts) == 1000 {
			return b, ErrServiceAlertImportLimit
		}
		var v PortableServiceAlert
		var ref ServiceAlertChannelReference
		if err := rows.Scan(&v.SourceID, &v.Name, &v.ChannelID, &ref.Name, &ref.Provider, &v.Settings.Enabled, &v.Settings.CertificateRemindersEnabled, &v.Settings.DurationSeconds, &v.Settings.RecoverySeconds, &v.Settings.RepeatSeconds, &v.Settings.CooldownSeconds); err != nil {
			return b, err
		}
		ref.ID = v.ChannelID
		b.Alerts = append(b.Alerts, v)
		if !seen[ref.ID] {
			seen[ref.ID] = true
			b.Channels = append(b.Channels, ref)
		}
	}
	if err := rows.Err(); err != nil {
		return b, err
	}
	return b, validateServiceAlertBundle(b, true)
}

func (s *Store) ServiceAlertDestinations(ctx context.Context) (ServiceAlertDestinations, error) {
	result := ServiceAlertDestinations{Services: []ServiceAlertDestination{}, Channels: []ServiceAlertDestination{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	for _, table := range []string{"http_services", "notification_channels"} {
		query := `SELECT id,name,enabled,'' FROM http_services ORDER BY id LIMIT 1001`
		if table == "notification_channels" {
			query = `SELECT id,name,enabled,COALESCE(NULLIF(provider,''),kind) FROM notification_channels ORDER BY id LIMIT 1001`
		}
		rows, err := tx.QueryContext(ctx, query)
		if err != nil {
			return result, err
		}
		items := []ServiceAlertDestination{}
		for rows.Next() {
			if len(items) == 1000 {
				rows.Close()
				return result, ErrServiceAlertImportLimit
			}
			var v ServiceAlertDestination
			if err := rows.Scan(&v.ID, &v.Name, &v.Enabled, &v.Provider); err != nil {
				rows.Close()
				return result, err
			}
			items = append(items, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return result, err
		}
		if table == "http_services" {
			result.Services = items
		} else {
			result.Channels = items
		}
	}
	return result, nil
}

func serviceAlertRequestDigest(bundle ServiceAlertBundle, mapping ServiceAlertImportMapping, preview string) (string, error) {
	if validateServiceAlertBundle(bundle, false) != nil || len(mapping.Services) != len(bundle.Alerts) || len(mapping.Channels) != len(bundle.Channels) {
		return "", ErrServiceAlertImportInvalid
	}
	if preview != "" {
		b, err := hex.DecodeString(preview)
		if err != nil || len(b) != 32 {
			return "", ErrServiceAlertImportInvalid
		}
	}
	return serviceAlertDigest(struct {
		Bundle  ServiceAlertBundle        `json:"bundle"`
		Mapping ServiceAlertImportMapping `json:"mapping"`
		Preview string                    `json:"preview_digest"`
	}{bundle, mapping, preview})
}
func serviceAlertDigest(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil || len(body) > 2<<20 {
		return "", ErrServiceAlertImportInvalid
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Store) PreviewServiceAlertImport(ctx context.Context, bundle ServiceAlertBundle, mapping ServiceAlertImportMapping) (ServiceAlertImportPreview, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ServiceAlertImportPreview{}, err
	}
	defer tx.Rollback()
	return prepareServiceAlertImport(ctx, tx, bundle, mapping)
}

func prepareServiceAlertImport(ctx context.Context, tx *sql.Tx, bundle ServiceAlertBundle, mapping ServiceAlertImportMapping) (ServiceAlertImportPreview, error) {
	result := ServiceAlertImportPreview{Items: []ServiceAlertImportItem{}}
	if _, err := serviceAlertRequestDigest(bundle, mapping, ""); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	for _, source := range bundle.Alerts {
		serviceID, channelID := mapping.Services[source.SourceID], mapping.Channels[source.ChannelID]
		if !portableID.MatchString(serviceID) || !portableID.MatchString(channelID) || seen[serviceID] {
			return result, ErrServiceAlertImportInvalid
		}
		seen[serviceID] = true
		item := ServiceAlertImportItem{SourceID: source.SourceID, ServiceID: serviceID, ChannelID: channelID, After: source.Settings}
		var before ServiceAlertParameters
		err := tx.QueryRowContext(ctx, `SELECT s.name,s.enabled,s.revision,s.updated_at,COALESCE(a.revision,0),COALESCE(a.updated_at,''),COALESCE(a.channel_id,''),COALESCE(c.name,''),COALESCE(NULLIF(c.provider,''),c.kind,''),COALESCE(a.enabled,0),COALESCE(a.certificate_reminders_enabled,0),COALESCE(a.duration_seconds,0),COALESCE(a.recovery_seconds,0),COALESCE(a.repeat_seconds,0),COALESCE(a.cooldown_seconds,300) FROM http_services s LEFT JOIN http_service_alerts a ON a.service_id=s.id LEFT JOIN notification_channels c ON c.id=a.channel_id WHERE s.id=?`, serviceID).Scan(&item.ServiceName, &item.ServiceEnabled, &item.ServiceRevision, &item.ServiceUpdatedAt, &item.AlertRevision, &item.AlertUpdatedAt, &item.BeforeChannelID, &item.BeforeChannelName, &item.BeforeChannelProvider, &before.Enabled, &before.CertificateRemindersEnabled, &before.DurationSeconds, &before.RecoverySeconds, &before.RepeatSeconds, &before.CooldownSeconds)
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrServiceAlertImportInvalid
		}
		if err != nil {
			return result, err
		}
		err = tx.QueryRowContext(ctx, `SELECT name,COALESCE(NULLIF(provider,''),kind),enabled,delivery_revision FROM notification_channels WHERE id=?`, channelID).Scan(&item.ChannelName, &item.ChannelProvider, &item.ChannelEnabled, &item.ChannelDeliveryRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrServiceAlertImportInvalid
		}
		if err != nil {
			return result, err
		}
		if !validChannelKind(item.ChannelProvider) || item.ServiceRevision < 1 || item.AlertRevision < 0 || item.AlertRevision >= 1<<63-1 {
			return result, ErrServiceAlertImportInvalid
		}
		if item.AlertRevision == 0 {
			result.Created++
		} else {
			item.Before = &before
			result.Updated++
		}
		result.Items = append(result.Items, item)
	}
	// Equal map lengths alone do not prove that every declared source was mapped.
	for _, ref := range bundle.Channels {
		if !portableID.MatchString(mapping.Channels[ref.ID]) {
			return result, ErrServiceAlertImportInvalid
		}
	}
	digest, err := serviceAlertDigest(struct {
		Bundle  ServiceAlertBundle        `json:"bundle"`
		Mapping ServiceAlertImportMapping `json:"mapping"`
		Items   []ServiceAlertImportItem  `json:"items"`
	}{bundle, mapping, result.Items})
	result.Digest = digest
	return result, err
}

// ApplyServiceAlertImport checks replay before mutable destinations. The writer
// lock covers preview revalidation, all setting updates and the replay record.
func (s *Store) ApplyServiceAlertImport(ctx context.Context, requestID string, bundle ServiceAlertBundle, mapping ServiceAlertImportMapping, preview string) (ServiceAlertImportResult, error) {
	result := ServiceAlertImportResult{ServiceIDs: []string{}}
	if !portableID.MatchString(requestID) || preview == "" {
		return result, ErrServiceAlertImportInvalid
	}
	digest, err := serviceAlertRequestDigest(bundle, mapping, preview)
	if err != nil {
		return result, err
	}
	// Committed receipts are permanent and immutable through the application.
	// A point read can return one without waiting for unrelated management writes.
	// Misses still take the writer and recheck below, closing concurrent commits.
	var stored, raw string
	err = s.db.QueryRowContext(ctx, `SELECT request_digest,result_json FROM service_alert_imports WHERE request_id=?`, requestID).Scan(&stored, &raw)
	if err == nil {
		return decodeServiceAlertReplay(stored, digest, raw)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return result, err
	}
	err = tx.QueryRowContext(ctx, `SELECT request_digest,result_json FROM service_alert_imports WHERE request_id=?`, requestID).Scan(&stored, &raw)
	if err == nil {
		result, err = decodeServiceAlertReplay(stored, digest, raw)
		if err != nil {
			return result, err
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM service_alert_imports`).Scan(&count); err != nil {
		return result, err
	}
	if count >= 10000 {
		return result, ErrServiceAlertImportLimit
	}
	prepared, err := prepareServiceAlertImport(ctx, tx, bundle, mapping)
	if err != nil {
		return result, err
	}
	if prepared.Digest != preview {
		return result, ErrServiceAlertImportConflict
	}
	now := nowText()
	for _, item := range prepared.Items {
		if _, err := saveServiceAlertSettings(ctx, tx, item.After.settings(item.ServiceID, item.ChannelID, item.ServiceRevision, item.AlertRevision), now); err != nil {
			return result, err
		}
		result.ServiceIDs = append(result.ServiceIDs, item.ServiceID)
	}
	result.Created, result.Updated = prepared.Created, prepared.Updated
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO service_alert_imports(request_id,request_digest,result_json,created_at) VALUES(?,?,?,?)`, requestID, digest, string(body), now); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func decodeServiceAlertReplay(stored, digest, raw string) (ServiceAlertImportResult, error) {
	result := ServiceAlertImportResult{ServiceIDs: []string{}}
	if stored != digest {
		return result, ErrServiceAlertImportRequestConflict
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		return result, errors.New("invalid service alert replay result")
	}
	result.Replayed = true
	return result, nil
}
