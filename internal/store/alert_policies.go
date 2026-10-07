package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

var ErrInvalidAlertPolicy = errors.New("invalid alert policy")
var ErrAlertPolicyConflict = errors.New("alert policy changed or overlaps an equal-priority policy")

type AlertPolicy struct {
	alertpolicy.Policy
	Name            string          `json:"name"`
	Revision        int64           `json:"revision"`
	ChannelID       string          `json:"channel_id"`
	Kind            string          `json:"kind"`
	Config          json.RawMessage `json:"config"`
	CooldownSeconds int             `json:"cooldown_seconds"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type policyQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listAlertPolicies(ctx context.Context, q policyQuery) ([]AlertPolicy, error) {
	rows, err := q.QueryContext(ctx, `SELECT definition_json FROM alert_policies ORDER BY id LIMIT 1001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AlertPolicy, 0)
	for rows.Next() {
		var raw string
		var p AlertPolicy
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	if len(items) > 1000 {
		return nil, errors.New("alert policy limit exceeded")
	}
	return items, rows.Err()
}

func (s *Store) ListAlertPolicies(ctx context.Context) ([]AlertPolicy, error) {
	return listAlertPolicies(ctx, s.db)
}

// SaveAlertPolicy serializes set validation with mutation across connections and
// processes. Caller revisions are compare-and-swap tokens; zero means create.
// Semantic threshold validation belongs to the alert service before this method.
func (s *Store) SaveAlertPolicy(ctx context.Context, p AlertPolicy) (AlertPolicy, error) {
	creating := p.ID == "" && p.Revision == 0
	if creating {
		p.ID = randomID()
	} else if p.ID == "" || p.Revision <= 0 || p.Revision == math.MaxInt64 {
		return AlertPolicy{}, ErrInvalidAlertPolicy
	}
	p.Name = strings.TrimSpace(p.Name)
	var object map[string]json.RawMessage
	if p.Name == "" || len(p.Name) > 128 || !utf8.ValidString(p.Name) || strings.IndexFunc(p.Name, unicode.IsControl) >= 0 ||
		!validAlertKind(p.Kind) || p.ChannelID == "" || len(p.Config) > 16384 || json.Unmarshal(p.Config, &object) != nil || object == nil ||
		p.CooldownSeconds < 30 || p.CooldownSeconds > 86400*30 || alertpolicy.ValidateSet([]alertpolicy.Policy{p.Policy}) != nil {
		return AlertPolicy{}, ErrInvalidAlertPolicy
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AlertPolicy{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return AlertPolicy{}, err
	}
	items, err := listAlertPolicies(ctx, tx)
	if err != nil {
		return AlertPolicy{}, err
	}
	if creating && len(items) >= 1000 {
		return AlertPolicy{}, ErrInvalidAlertPolicy
	}
	policies := make([]alertpolicy.Policy, 0, len(items)+1)
	found := false
	p.CreatedAt = time.Now().UTC()
	p.UpdatedAt = p.CreatedAt
	for _, existing := range items {
		if existing.ID == p.ID {
			if creating || existing.Revision != p.Revision {
				return AlertPolicy{}, ErrAlertPolicyConflict
			}
			found = true
			p.CreatedAt = existing.CreatedAt
		} else {
			policies = append(policies, existing.Policy)
		}
	}
	if !creating && !found {
		return AlertPolicy{}, ErrAlertPolicyConflict
	}
	policies = append(policies, p.Policy)
	if alertpolicy.ValidateSet(policies) != nil {
		return AlertPolicy{}, ErrAlertPolicyConflict
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM notification_channels WHERE id=?`, p.ChannelID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AlertPolicy{}, ErrInvalidAlertPolicy
		}
		return AlertPolicy{}, err
	}
	for _, id := range p.Scope.NodeIDs {
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id=?`, id).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return AlertPolicy{}, ErrInvalidAlertPolicy
			}
			return AlertPolicy{}, err
		}
	}
	p.Revision++
	raw, err := json.Marshal(p)
	if err != nil {
		return AlertPolicy{}, err
	}
	if creating {
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_policies(id,revision,channel_id,definition_json,created_at,updated_at) VALUES(?,?,?,?,?,?)`, p.ID, p.Revision, p.ChannelID, string(raw), formatTime(p.CreatedAt), formatTime(p.UpdatedAt))
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE alert_policies SET revision=?,channel_id=?,definition_json=?,updated_at=? WHERE id=? AND revision=?`, p.Revision, p.ChannelID, string(raw), formatTime(p.UpdatedAt), p.ID, p.Revision-1)
	}
	if err != nil {
		return AlertPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return AlertPolicy{}, err
	}
	// Round-trip to detach slices from caller-owned input.
	var saved AlertPolicy
	if err = json.Unmarshal(raw, &saved); err != nil {
		return AlertPolicy{}, err
	}
	return saved, nil
}

func (s *Store) DeleteAlertPolicy(ctx context.Context, id string, revision int64) error {
	if id == "" || revision <= 0 {
		return ErrInvalidAlertPolicy
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM alert_policies WHERE id=? AND revision=?`, id, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAlertPolicyConflict
	}
	return nil
}

// EffectiveAlertPolicies previews one consistent snapshot of tags and policies.
func (s *Store) EffectiveAlertPolicies(ctx context.Context, nodeID string) ([]alertpolicy.Decision, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var raw string
	if err = tx.QueryRowContext(ctx, `SELECT tags_json FROM nodes WHERE id=?`, nodeID).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var tags []string
	if err = json.Unmarshal([]byte(raw), &tags); err != nil {
		return nil, err
	}
	items, err := listAlertPolicies(ctx, tx)
	if err != nil {
		return nil, err
	}
	policies := make([]alertpolicy.Policy, 0, len(items))
	for _, p := range items {
		policies = append(policies, p.Policy)
	}
	return alertpolicy.Resolve(policies, alertpolicy.Node{ID: nodeID, Tags: tags})
}
