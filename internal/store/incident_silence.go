package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrInvalidSilence = errors.New("invalid incident silence")
var ErrSilenceRequestConflict = errors.New("silence request reused with different content")
var ErrSilenceActive = errors.New("incident already has an overlapping silence")
var ErrSilenceConflict = errors.New("incident silence changed or ended")

type IncidentSilence struct {
	ID                   string     `json:"id"`
	IncidentID           string     `json:"incident_id"`
	Revision             int64      `json:"revision"`
	StartsAt             time.Time  `json:"starts_at"`
	EndsAt               time.Time  `json:"ends_at"`
	Reason               string     `json:"reason"`
	CreatedBy            string     `json:"created_by"`
	CreatorName          string     `json:"creator_name"`
	AcknowledgeRequested bool       `json:"acknowledge_requested"`
	CanceledAt           *time.Time `json:"canceled_at,omitempty"`
	CanceledBy           string     `json:"canceled_by,omitempty"`
	CancelerName         string     `json:"canceler_name,omitempty"`
	ClosedAt             *time.Time `json:"closed_at,omitempty"`
	ClosureReason        string     `json:"closure_reason,omitempty"`
}
type CreateIncidentSilenceParams struct {
	DurationSeconds int    `json:"duration_seconds"`
	Reason          string `json:"reason"`
	Acknowledge     bool   `json:"acknowledge"`
}
type SilenceMutationResult struct {
	SilenceID              string     `json:"silence_id"`
	Revision               int64      `json:"revision"`
	StartsAt               time.Time  `json:"starts_at"`
	EndsAt                 time.Time  `json:"ends_at"`
	CanceledAt             *time.Time `json:"canceled_at,omitempty"`
	AcknowledgementCreated bool       `json:"acknowledgement_created"`
	Replayed               bool       `json:"replayed"`
}

const silenceColumns = `id,incident_id,revision,starts_at_ms,ends_at_ms,reason,created_by,creator_name,acknowledge_requested,canceled_at_ms,canceled_by,canceler_name,closed_at_ms,closure_reason`
const activeSilencePredicate = `s.starts_at_ms<=? AND s.ends_at_ms>? AND (s.canceled_at_ms IS NULL OR s.canceled_at_ms>?) AND (s.closed_at_ms IS NULL OR s.closed_at_ms>?)`
const overlappingSilencePredicate = `s.starts_at_ms<? AND s.ends_at_ms>? AND (s.canceled_at_ms IS NULL OR (s.canceled_at_ms>s.starts_at_ms AND s.canceled_at_ms>?)) AND (s.closed_at_ms IS NULL OR (s.closed_at_ms>s.starts_at_ms AND s.closed_at_ms>?))`

func scanSilence(row rowScanner) (IncidentSilence, error) {
	var s IncidentSilence
	var start, end int64
	var canceled, closed sql.NullInt64
	err := row.Scan(&s.ID, &s.IncidentID, &s.Revision, &start, &end, &s.Reason, &s.CreatedBy, &s.CreatorName, &s.AcknowledgeRequested, &canceled, &s.CanceledBy, &s.CancelerName, &closed, &s.ClosureReason)
	s.StartsAt, s.EndsAt = time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC()
	if canceled.Valid {
		at := time.UnixMilli(canceled.Int64).UTC()
		s.CanceledAt = &at
	}
	if closed.Valid {
		at := time.UnixMilli(closed.Int64).UTC()
		s.ClosedAt = &at
	}
	return s, err
}
func validSilenceTime(at time.Time) bool {
	return !at.IsZero() && at.UTC().Year() >= 1970 && at.UTC().Year() <= 9999
}
func silenceRequestDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func replaySilenceRequest(ctx context.Context, tx *sql.Tx, requestID, digest string) (SilenceMutationResult, bool, error) {
	var result SilenceMutationResult
	var previous, raw string
	err := tx.QueryRowContext(ctx, `SELECT request_digest,result_json FROM incident_silence_requests WHERE request_id=?`, requestID).Scan(&previous, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if digest != previous {
		return result, false, ErrSilenceRequestConflict
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return result, false, err
	}
	result.Replayed = true
	return result, true, nil
}
func saveSilenceRequest(ctx context.Context, tx *sql.Tx, requestID, digest string, result SilenceMutationResult, now time.Time) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO incident_silence_requests(request_id,request_digest,silence_id,result_json,created_at) VALUES(?,?,?,?,?)`, requestID, digest, result.SilenceID, string(raw), formatTime(now))
	return err
}
func silenceActorName(ctx context.Context, tx *sql.Tx, actor string) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id=?`, actor).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalidSilence
	}
	return name, err
}

// Server supplies actor/now. Setting silence suppresses outstanding intent in the
// same transaction; condition state and observation/continuity timestamps stay intact.
func (s *Store) CreateIncidentSilence(ctx context.Context, incidentID, requestID, actor string, p CreateIncidentSilenceParams, now time.Time) (SilenceMutationResult, error) {
	var empty SilenceMutationResult
	reason, err := normalizeAcknowledgementNote(p.Reason)
	if err != nil || p.DurationSeconds < 60 || p.DurationSeconds > 7*24*3600 || !portableID.MatchString(incidentID) || !portableID.MatchString(requestID) || !portableID.MatchString(actor) || !validSilenceTime(now) {
		return empty, ErrInvalidSilence
	}
	p.Reason = reason
	actionAt := now.UTC()
	now = actionAt.Truncate(time.Millisecond)
	until := now.Add(time.Duration(p.DurationSeconds) * time.Second)
	if !validSilenceTime(until) {
		return empty, ErrInvalidSilence
	}
	digest := silenceRequestDigest(struct {
		Operation, IncidentID, Actor string
		Params                       CreateIncidentSilenceParams
	}{"create", incidentID, actor, p})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET updated_at=updated_at WHERE id=?`, incidentID); err != nil {
		return empty, err
	}
	if replay, found, err := replaySilenceRequest(ctx, tx, requestID, digest); found || err != nil {
		return replay, err
	}
	i, err := scanIncident(tx.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM alert_incidents WHERE id=?`, incidentID))
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if i.State != "firing" {
		return empty, ErrIncidentNotFiring
	}
	if actionAt.Before(i.UpdatedAt) {
		return empty, ErrSilenceConflict
	}
	ids, err := matchingSilenceIDs(ctx, tx, incidentID, now, until, true)
	if err != nil {
		return empty, err
	}
	if len(ids) > 0 {
		return empty, ErrSilenceActive
	}
	name, err := silenceActorName(ctx, tx, actor)
	if err != nil {
		return empty, err
	}
	id := randomID()
	if _, err := tx.ExecContext(ctx, `INSERT INTO incident_silences(id,incident_id,starts_at_ms,ends_at_ms,reason,created_by,creator_name,acknowledge_requested) VALUES(?,?,?,?,?,?,?,?)`, id, incidentID, now.UnixMilli(), until.UnixMilli(), reason, actor, name, p.Acknowledge); err != nil {
		return empty, err
	}
	result := SilenceMutationResult{SilenceID: id, Revision: 1, StartsAt: now, EndsAt: until}
	if p.Acknowledge {
		var acknowledged bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM incident_acknowledgements WHERE incident_id=?)`, incidentID).Scan(&acknowledged); err != nil {
			return empty, err
		}
		if !acknowledged {
			// Colon reserves an internal namespace which standalone ACK IDs cannot use.
			ackKey := "silence:" + requestID
			if _, err := insertAcknowledgement(ctx, tx, incidentID, ackKey, reason, actor, name, now); err != nil {
				return empty, err
			}
			result.AcknowledgementCreated = true
		}
	}
	var outstanding bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE incident_id=? AND status IN ('pending','inflight'))`, incidentID).Scan(&outstanding); err != nil {
		return empty, err
	}
	var maintenanceIDs []string
	if outstanding {
		maintenanceIDs, err = matchingMaintenanceIDs(ctx, tx, i.NodeID, time.Time{}, now, false)
		if err != nil {
			return empty, err
		}
	}
	cause, err := recordNotificationSuppression(ctx, tx, incidentID, maintenanceIDs, []string{id}, "creation", now, "")
	if err != nil {
		return empty, err
	}
	if err := cancelIncidentDeliveries(ctx, tx, incidentID, cause, now); err != nil {
		return empty, err
	}
	// Reject observations captured before the action if they acquire the writer
	// afterwards. This is a mutation watermark, not a fresh metric observation.
	i.UpdatedAt, i.LastEnqueuedAt = actionAt, nil
	if err := saveIncident(ctx, tx, &i); err != nil {
		return empty, err
	}
	details, _ := json.Marshal(struct {
		DurationSeconds        int  `json:"duration_seconds"`
		AcknowledgementCreated bool `json:"acknowledgement_created"`
	}{p.DurationSeconds, result.AcknowledgementCreated})
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, actor, "create", "incident_silence", id, "", string(details), formatTime(now)); err != nil {
		return empty, err
	}
	if err := saveSilenceRequest(ctx, tx, requestID, digest, result, now); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func (s *Store) CancelIncidentSilence(ctx context.Context, incidentID, id, requestID, actor string, revision int64, now time.Time) (SilenceMutationResult, error) {
	var empty SilenceMutationResult
	if !portableID.MatchString(incidentID) || !portableID.MatchString(id) || !portableID.MatchString(requestID) || !portableID.MatchString(actor) || revision < 1 || !validSilenceTime(now) {
		return empty, ErrInvalidSilence
	}
	actionAt := now.UTC()
	now = actionAt.Truncate(time.Millisecond)
	digest := silenceRequestDigest(struct {
		Operation, IncidentID, SilenceID, Actor string
		Revision                                int64
	}{"cancel", incidentID, id, actor, revision})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET updated_at=updated_at WHERE id=?`, incidentID); err != nil {
		return empty, err
	}
	if replay, found, err := replaySilenceRequest(ctx, tx, requestID, digest); found || err != nil {
		return replay, err
	}
	window, err := scanSilence(tx.QueryRowContext(ctx, `SELECT `+silenceColumns+` FROM incident_silences WHERE id=? AND incident_id=?`, id, incidentID))
	if errors.Is(err, sql.ErrNoRows) {
		return empty, ErrNotFound
	}
	if err != nil {
		return empty, err
	}
	if window.Revision != revision || window.CanceledAt != nil || window.ClosedAt != nil || now.Before(window.StartsAt) || !now.Before(window.EndsAt) {
		return empty, ErrSilenceConflict
	}
	i, err := scanIncident(tx.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM alert_incidents WHERE id=?`, incidentID))
	if err != nil {
		return empty, err
	}
	if i.State != "firing" || actionAt.Before(i.UpdatedAt) {
		return empty, ErrSilenceConflict
	}
	name, err := silenceActorName(ctx, tx, actor)
	if err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE incident_silences SET revision=revision+1,canceled_at_ms=?,canceled_by=?,canceler_name=? WHERE id=?`, now.UnixMilli(), actor, name, id); err != nil {
		return empty, err
	}
	i.UpdatedAt, i.LastEnqueuedAt = actionAt, nil
	if err := saveIncident(ctx, tx, &i); err != nil {
		return empty, err
	}
	result := SilenceMutationResult{SilenceID: id, Revision: revision + 1, StartsAt: window.StartsAt, EndsAt: window.EndsAt, CanceledAt: &now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, actor, "cancel", "incident_silence", id, "", `{}`, formatTime(now)); err != nil {
		return empty, err
	}
	if err := saveSilenceRequest(ctx, tx, requestID, digest, result, now); err != nil {
		return empty, err
	}
	if err := tx.Commit(); err != nil {
		return empty, err
	}
	return result, nil
}

func closeIncidentSilences(ctx context.Context, tx *sql.Tx, incidentID, reason string, at time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE incident_silences SET revision=revision+1,closed_at_ms=?,closure_reason=? WHERE incident_id=? AND canceled_at_ms IS NULL AND closed_at_ms IS NULL AND starts_at_ms<=? AND ends_at_ms>?`, at.UnixMilli(), reason, incidentID, at.UnixMilli(), at.UnixMilli())
	return err
}
