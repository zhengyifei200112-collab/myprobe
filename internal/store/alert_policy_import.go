package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrPolicyImportConflict = errors.New("policy import request ID was used for different content")
var ErrPolicyImportLimit = errors.New("policy import history limit reached")

type PolicyImportResult struct {
	PolicyIDs []string `json:"policy_ids"`
	Replayed  bool     `json:"replayed"`
}

// LookupPolicyImport lets the service replay before validating mutable references.
// ApplyPolicyImport must still check again inside its writer transaction.
func (s *Store) LookupPolicyImport(ctx context.Context, requestID, digest string) (*PolicyImportResult, error) {
	decoded, err := hex.DecodeString(digest)
	if !portableID.MatchString(requestID) || err != nil || len(decoded) != 32 {
		return nil, ErrInvalidAlertPolicy
	}
	var existing, raw string
	err = s.db.QueryRowContext(ctx, `SELECT request_digest,policy_ids_json FROM alert_policy_imports WHERE request_id=?`, requestID).Scan(&existing, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if existing != digest {
		return nil, ErrPolicyImportConflict
	}
	result := &PolicyImportResult{Replayed: true}
	if err = json.Unmarshal([]byte(raw), &result.PolicyIDs); err != nil {
		return nil, err
	}
	return result, nil
}

// ApplyPolicyImport commits definitions and their replay record together. Digest
// must cover the complete canonical request (bundle, mappings and import options).
// Returned IDs are the original result, even if those policies were later removed.
func (s *Store) ApplyPolicyImport(ctx context.Context, requestID, digest string, policies []AlertPolicy) (PolicyImportResult, error) {
	decoded, err := hex.DecodeString(digest)
	if !portableID.MatchString(requestID) || len(decoded) != 32 || err != nil || len(policies) == 0 || len(policies) > 1000 {
		return PolicyImportResult{}, ErrInvalidAlertPolicy
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PolicyImportResult{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return PolicyImportResult{}, err
	}
	var existing, raw string
	err = tx.QueryRowContext(ctx, `SELECT request_digest,policy_ids_json FROM alert_policy_imports WHERE request_id=?`, requestID).Scan(&existing, &raw)
	if err == nil {
		if existing != digest {
			return PolicyImportResult{}, ErrPolicyImportConflict
		}
		var result PolicyImportResult
		if err = json.Unmarshal([]byte(raw), &result.PolicyIDs); err != nil {
			return PolicyImportResult{}, err
		}
		result.Replayed = true
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PolicyImportResult{}, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_policy_imports`).Scan(&count); err != nil {
		return PolicyImportResult{}, err
	}
	if count >= 10000 {
		return PolicyImportResult{}, ErrPolicyImportLimit
	}
	result := PolicyImportResult{PolicyIDs: make([]string, 0, len(policies))}
	for _, p := range policies {
		if p.ID != "" || p.Revision != 0 {
			return PolicyImportResult{}, ErrInvalidAlertPolicy
		}
		p, err = saveAlertPolicy(ctx, tx, p)
		if err != nil {
			return PolicyImportResult{}, err
		}
		result.PolicyIDs = append(result.PolicyIDs, p.ID)
	}
	encoded, err := json.Marshal(result.PolicyIDs)
	if err != nil {
		return PolicyImportResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO alert_policy_imports(request_id,request_digest,policy_ids_json,created_at) VALUES(?,?,?,?)`, requestID, digest, string(encoded), nowText()); err != nil {
		return PolicyImportResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return PolicyImportResult{}, err
	}
	return result, nil
}
