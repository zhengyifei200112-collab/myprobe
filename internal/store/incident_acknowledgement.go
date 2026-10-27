package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidAcknowledgement = errors.New("invalid incident acknowledgement")
var ErrAcknowledgementRequestConflict = errors.New("acknowledgement request reused with different content")
var ErrIncidentAlreadyAcknowledged = errors.New("incident already acknowledged")
var ErrIncidentNotFiring = errors.New("incident is not firing")

type IncidentAcknowledgement struct {
	IncidentID     string    `json:"incident_id"`
	ActorID        string    `json:"actor_id"`
	ActorName      string    `json:"actor_name"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
	Note           string    `json:"note"`
}
type IncidentAcknowledgementSummary struct {
	ActorName      string    `json:"actor_name"`
	AcknowledgedAt time.Time `json:"acknowledged_at"`
}
type IncidentAcknowledgementView struct {
	IncidentState   string                   `json:"incident_state"`
	Acknowledgement *IncidentAcknowledgement `json:"acknowledgement"`
}
type AcknowledgeIncidentResult struct {
	Acknowledgement IncidentAcknowledgement `json:"acknowledgement"`
	Replayed        bool                    `json:"replayed"`
}

func normalizeAcknowledgementNote(note string) (string, error) {
	if !utf8.ValidString(note) {
		return "", ErrInvalidAcknowledgement
	}
	note = strings.TrimSpace(strings.ReplaceAll(note, "\r\n", "\n"))
	if len(note) > 1000 {
		return "", ErrInvalidAcknowledgement
	}
	for _, r := range note {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", ErrInvalidAcknowledgement
		}
	}
	return note, nil
}

const acknowledgementColumns = `incident_id,actor_id,actor_name,acknowledged_at,note`

func scanAcknowledgement(row rowScanner) (IncidentAcknowledgement, error) {
	var a IncidentAcknowledgement
	var at string
	err := row.Scan(&a.IncidentID, &a.ActorID, &a.ActorName, &at, &a.Note)
	if err != nil {
		return a, err
	}
	a.AcknowledgedAt, err = parseTime(at)
	return a, err
}

// The authenticated actor is supplied by the server, never the client payload.
// First acknowledgement wins. Replay is checked before mutable incident state;
// no incident, delivery, repeat clock or maintenance field is changed.
func (s *Store) AcknowledgeIncident(ctx context.Context, incidentID, requestID, note, actor string) (AcknowledgeIncidentResult, error) {
	note, err := normalizeAcknowledgementNote(note)
	if err != nil || !portableID.MatchString(incidentID) || !portableID.MatchString(requestID) || !portableID.MatchString(actor) {
		return AcknowledgeIncidentResult{}, ErrInvalidAcknowledgement
	}
	digest := acknowledgementDigest(incidentID, actor, note)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	defer tx.Rollback()
	// Reserve the SQLite writer before reading either first-winner or retry state.
	if _, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET updated_at=updated_at WHERE id=?`, incidentID); err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT request_digest FROM incident_acknowledgements WHERE request_id=?`, requestID).Scan(&previous)
	if err == nil {
		if previous != digest {
			return AcknowledgeIncidentResult{}, ErrAcknowledgementRequestConflict
		}
		a, err := scanAcknowledgement(tx.QueryRowContext(ctx, `SELECT `+acknowledgementColumns+` FROM incident_acknowledgements WHERE request_id=?`, requestID))
		return AcknowledgeIncidentResult{Acknowledgement: a, Replayed: true}, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AcknowledgeIncidentResult{}, err
	}
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM alert_incidents WHERE id=?`, incidentID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return AcknowledgeIncidentResult{}, ErrNotFound
	}
	if err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	var existing bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM incident_acknowledgements WHERE incident_id=?)`, incidentID).Scan(&existing); err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	if existing {
		return AcknowledgeIncidentResult{}, ErrIncidentAlreadyAcknowledged
	}
	if state != "firing" {
		return AcknowledgeIncidentResult{}, ErrIncidentNotFiring
	}
	a := IncidentAcknowledgement{IncidentID: incidentID, ActorID: actor, Note: note}
	err = tx.QueryRowContext(ctx, `SELECT username FROM users WHERE id=?`, actor).Scan(&a.ActorName)
	if errors.Is(err, sql.ErrNoRows) {
		return AcknowledgeIncidentResult{}, ErrInvalidAcknowledgement
	}
	if err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	a, err = insertAcknowledgement(ctx, tx, incidentID, requestID, note, actor, a.ActorName, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AcknowledgeIncidentResult{}, err
	}
	return AcknowledgeIncidentResult{Acknowledgement: a}, nil
}

func acknowledgementDigest(incidentID, actor, note string) string {
	raw, _ := json.Marshal(struct{ IncidentID, Actor, Note string }{incidentID, actor, note})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Caller holds the writer and has checked state, normalized note and actor.
// Combined silence/ACK uses this helper so neither half can commit alone.
func insertAcknowledgement(ctx context.Context, tx *sql.Tx, incidentID, requestID, note, actor, actorName string, now time.Time) (IncidentAcknowledgement, error) {
	a := IncidentAcknowledgement{IncidentID: incidentID, ActorID: actor, ActorName: actorName, AcknowledgedAt: now, Note: note}
	if _, err := tx.ExecContext(ctx, `INSERT INTO incident_acknowledgements(incident_id,actor_id,actor_name,acknowledged_at,note,request_id,request_digest) VALUES(?,?,?,?,?,?,?)`, incidentID, actor, actorName, formatTime(now), note, requestID, acknowledgementDigest(incidentID, actor, note)); err != nil {
		return a, err
	}
	details, _ := json.Marshal(struct {
		HasNote bool `json:"has_note"`
	}{note != ""})
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, actor, "acknowledge", "alert_incident", incidentID, "", string(details), formatTime(now))
	return a, err
}

func (s *Store) IncidentAcknowledgement(ctx context.Context, incidentID string) (IncidentAcknowledgementView, error) {
	var view IncidentAcknowledgementView
	if !portableID.MatchString(incidentID) {
		return view, ErrInvalidAcknowledgement
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return view, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT state FROM alert_incidents WHERE id=?`, incidentID).Scan(&view.IncidentState)
	if errors.Is(err, sql.ErrNoRows) {
		return view, ErrNotFound
	}
	if err != nil {
		return view, err
	}
	a, err := scanAcknowledgement(tx.QueryRowContext(ctx, `SELECT `+acknowledgementColumns+` FROM incident_acknowledgements WHERE incident_id=?`, incidentID))
	if err == nil {
		view.Acknowledgement = &a
	} else if !errors.Is(err, sql.ErrNoRows) {
		return view, err
	}
	return view, tx.Commit()
}

// Fill private list summaries in the same read snapshot; notes and retry identity
// are not selected. One bounded query covers the at-most-100 incidents on a page.
func incidentAcknowledgementSummaries(ctx context.Context, tx *sql.Tx, items []Incident) error {
	if len(items) == 0 {
		return nil
	}
	positions := make(map[string]int, len(items))
	args := make([]any, len(items))
	for i := range items {
		positions[items[i].ID], args[i] = i, items[i].ID
	}
	query := `SELECT incident_id,actor_name,acknowledged_at FROM incident_acknowledgements WHERE incident_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(items)), ",") + `)`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, at string
		var a IncidentAcknowledgementSummary
		if err := rows.Scan(&id, &a.ActorName, &at); err != nil {
			return err
		}
		a.AcknowledgedAt, err = parseTime(at)
		if err != nil {
			return err
		}
		items[positions[id]].Acknowledgement = &a
	}
	return rows.Err()
}
