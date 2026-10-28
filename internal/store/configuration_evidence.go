package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

var ErrConfigurationSuperseded = errors.New("configuration_superseded")
var ErrConfigurationUnsupported = errors.New("configuration_acknowledgement_unsupported")

type DesiredSamplingConfiguration struct {
	Revision                         int64
	CollectionSeconds, ReportSeconds int
}

func (s *Store) DesiredSamplingConfiguration(ctx context.Context, nodeID string) (DesiredSamplingConfiguration, error) {
	var c DesiredSamplingConfiguration
	err := s.db.QueryRowContext(ctx, `SELECT agent_config_revision,collection_seconds,report_seconds FROM nodes WHERE id=?`, nodeID).Scan(&c.Revision, &c.CollectionSeconds, &c.ReportSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}
func runtimeNonce() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func desiredSamplingInTx(ctx context.Context, tx *sql.Tx, nodeID string) (DesiredSamplingConfiguration, error) {
	// Acquire the writer before reading identity and committing an issued token.
	result, err := tx.ExecContext(ctx, `UPDATE nodes SET agent_config_revision=agent_config_revision WHERE id=?`, nodeID)
	if err != nil {
		return DesiredSamplingConfiguration{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return DesiredSamplingConfiguration{}, err
	} else if n != 1 {
		return DesiredSamplingConfiguration{}, ErrNotFound
	}
	var c DesiredSamplingConfiguration
	err = tx.QueryRowContext(ctx, `SELECT agent_config_revision,collection_seconds,report_seconds FROM nodes WHERE id=?`, nodeID).Scan(&c.Revision, &c.CollectionSeconds, &c.ReportSeconds)
	return c, err
}

// BeginAgentConnection resets proof for the new connection generation. Callers
// serialize registrations, then publish the corresponding socket generation.
func (s *Store) BeginAgentConnection(ctx context.Context, nodeID, epoch string, wire int, supported bool, now time.Time) (*v2.RuntimeConfiguration, error) {
	if len(epoch) != 32 || now.IsZero() || (wire != 1 && wire != 2) || (supported && wire != 2) {
		return nil, errors.New("invalid_agent_connection")
	}
	if _, err := hex.DecodeString(epoch); err != nil {
		return nil, errors.New("invalid_agent_connection")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	desired, err := desiredSamplingInTx(ctx, tx, nodeID)
	if err != nil {
		return nil, err
	}
	var nonce, revision any
	state := "none"
	var configuration *v2.RuntimeConfiguration
	if supported {
		value, err := runtimeNonce()
		if err != nil {
			return nil, err
		}
		nonce = value
		revision = desired.Revision
		state = "prepared"
		configuration = &v2.RuntimeConfiguration{Scope: v2.SamplingConfigurationScope, Revision: desired.Revision, Nonce: value, CollectionSeconds: desired.CollectionSeconds, ReportSeconds: desired.ReportSeconds}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO node_configuration_evidence(node_id,connection_epoch,wire_version,acknowledgement_supported,connected_at,issued_nonce,issued_revision,dispatch_state) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET connection_epoch=excluded.connection_epoch,wire_version=excluded.wire_version,acknowledgement_supported=excluded.acknowledgement_supported,connected_at=excluded.connected_at,issued_nonce=excluded.issued_nonce,issued_revision=excluded.issued_revision,dispatch_state=excluded.dispatch_state,dispatched_at=NULL,applied_revision=NULL,applied_collection_seconds=NULL,applied_report_seconds=NULL,acknowledged_at=NULL`, nodeID, epoch, wire, supported, formatTime(now), nonce, revision, state)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return configuration, nil
}
func (s *Store) PrepareSamplingConfiguration(ctx context.Context, nodeID, epoch string) (v2.RuntimeConfiguration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v2.RuntimeConfiguration{}, err
	}
	defer tx.Rollback()
	desired, err := desiredSamplingInTx(ctx, tx, nodeID)
	if err != nil {
		return v2.RuntimeConfiguration{}, err
	}
	nonce, err := runtimeNonce()
	if err != nil {
		return v2.RuntimeConfiguration{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE node_configuration_evidence SET issued_nonce=?,issued_revision=?,dispatch_state='prepared',dispatched_at=NULL WHERE node_id=? AND connection_epoch=? AND acknowledgement_supported=1`, nonce, desired.Revision, nodeID, epoch)
	if err != nil {
		return v2.RuntimeConfiguration{}, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return v2.RuntimeConfiguration{}, err
	} else if n != 1 {
		return v2.RuntimeConfiguration{}, ErrConfigurationSuperseded
	}
	if err = tx.Commit(); err != nil {
		return v2.RuntimeConfiguration{}, err
	}
	return v2.RuntimeConfiguration{Scope: v2.SamplingConfigurationScope, Revision: desired.Revision, Nonce: nonce, CollectionSeconds: desired.CollectionSeconds, ReportSeconds: desired.ReportSeconds}, nil
}
func (s *Store) CompleteSamplingDispatch(ctx context.Context, nodeID, epoch, nonce string, sent bool, now time.Time) error {
	state := "failed"
	var dispatched any
	if sent {
		state = "sent"
		dispatched = formatTime(now)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE node_configuration_evidence SET dispatch_state=?,dispatched_at=? WHERE node_id=? AND connection_epoch=? AND issued_nonce=?`, state, dispatched, nodeID, epoch, nonce)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrConfigurationSuperseded
	}
	return nil
}
func (s *Store) AcknowledgeSamplingConfiguration(ctx context.Context, nodeID, epoch string, ack v2.ConfigurationApplied, now time.Time) error {
	if err := ack.Validate(); err != nil {
		return err
	}
	if now.IsZero() {
		return errors.New("invalid_configuration_acknowledgement")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	desired, err := desiredSamplingInTx(ctx, tx, nodeID)
	if err != nil {
		return err
	}
	if desired.Revision != ack.Revision || desired.CollectionSeconds != ack.CollectionSeconds || desired.ReportSeconds != ack.ReportSeconds {
		return ErrConfigurationSuperseded
	}
	result, err := tx.ExecContext(ctx, `UPDATE node_configuration_evidence SET applied_revision=?,applied_collection_seconds=?,applied_report_seconds=?,acknowledged_at=CASE WHEN applied_revision=? THEN COALESCE(acknowledged_at,?) ELSE ? END WHERE node_id=? AND connection_epoch=? AND acknowledgement_supported=1 AND issued_nonce=? AND issued_revision=? AND dispatch_state IN ('prepared','sent')`, ack.Revision, ack.CollectionSeconds, ack.ReportSeconds, ack.Revision, formatTime(now), formatTime(now), nodeID, epoch, ack.Nonce, ack.Revision)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil {
		return err
	} else if n != 1 {
		return ErrConfigurationSuperseded
	}
	return tx.Commit()
}

type SamplingConfigurationEvidence struct {
	ConnectionEpoch          string     `json:"-"`
	Status                   string     `json:"status"`
	Scope                    string     `json:"scope"`
	DesiredRevision          int64      `json:"desired_revision"`
	CollectionSeconds        int        `json:"collection_seconds"`
	ReportSeconds            int        `json:"report_seconds"`
	WireVersion              *int       `json:"wire_version,omitempty"`
	AcknowledgementSupported *bool      `json:"acknowledgement_supported,omitempty"`
	ConnectedAt              *time.Time `json:"connected_at,omitempty"`
	DispatchedAt             *time.Time `json:"dispatched_at,omitempty"`
	AppliedRevision          *int64     `json:"applied_revision,omitempty"`
	AcknowledgedAt           *time.Time `json:"acknowledged_at,omitempty"`
}

func (s *Store) SamplingConfigurationEvidence(ctx context.Context, nodeID string) (SamplingConfigurationEvidence, error) {
	result := SamplingConfigurationEvidence{Status: "no_connection_evidence", Scope: v2.SamplingConfigurationScope}
	var wire, issued, applied, supported sql.NullInt64
	var connected, dispatched, acknowledged, state sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT n.agent_config_revision,n.collection_seconds,n.report_seconds,COALESCE(e.connection_epoch,''),e.wire_version,e.acknowledgement_supported,e.connected_at,e.issued_revision,e.dispatch_state,e.dispatched_at,e.applied_revision,e.acknowledged_at FROM nodes n LEFT JOIN node_configuration_evidence e ON e.node_id=n.id WHERE n.id=?`, nodeID).Scan(&result.DesiredRevision, &result.CollectionSeconds, &result.ReportSeconds, &result.ConnectionEpoch, &wire, &supported, &connected, &issued, &state, &dispatched, &applied, &acknowledged)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if !wire.Valid {
		return result, nil
	}
	version := int(wire.Int64)
	result.WireVersion = &version
	capable := supported.Int64 == 1
	result.AcknowledgementSupported = &capable
	for _, item := range []struct {
		value  sql.NullString
		target **time.Time
	}{{connected, &result.ConnectedAt}, {dispatched, &result.DispatchedAt}, {acknowledged, &result.AcknowledgedAt}} {
		if item.value.Valid {
			at, e := parseTime(item.value.String)
			if e != nil {
				return result, e
			}
			*item.target = &at
		}
	}
	if applied.Valid {
		revision := applied.Int64
		result.AppliedRevision = &revision
	}
	if !capable {
		result.Status = "acknowledgement_unsupported"
	} else if applied.Valid && applied.Int64 == result.DesiredRevision {
		result.Status = "agent_confirmed"
	} else if !issued.Valid || issued.Int64 != result.DesiredRevision {
		result.Status = "awaiting_dispatch"
	} else {
		switch state.String {
		case "prepared":
			result.Status = "prepared"
		case "sent":
			result.Status = "awaiting_agent_ack"
		case "failed":
			result.Status = "dispatch_failed"
		default:
			result.Status = "awaiting_dispatch"
		}
	}
	return result, nil
}
