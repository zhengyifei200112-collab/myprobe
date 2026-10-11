package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

type MaintenanceDraft struct {
	Selection MaintenanceSelection `json:"selection"`
	Reason    string               `json:"reason"`
	Timezone  string               `json:"timezone"`
	StartsAt  time.Time            `json:"starts_at"`
	EndsAt    time.Time            `json:"ends_at"`
}

type MaintenancePreview struct {
	Draft   MaintenanceDraft    `json:"draft"`
	Members []MaintenanceMember `json:"members"`
	Digest  string              `json:"preview_digest"`
}

// PreviewMaintenanceWindow returns normalized confirmation content. The digest
// detects content/member changes, not authorization or proof a preview was seen.
func (s *Store) PreviewMaintenanceWindow(ctx context.Context, draft MaintenanceDraft) (MaintenancePreview, error) {
	return previewMaintenanceWindow(ctx, s.db, draft)
}

func previewMaintenanceWindow(ctx context.Context, q maintenanceSelectionQuery, draft MaintenanceDraft) (MaintenancePreview, error) {
	members, err := resolveMaintenanceMembers(ctx, q, draft.Selection)
	if err != nil {
		return MaintenancePreview{}, err
	}
	ids := make([]string, len(members))
	for i, member := range members {
		ids[i] = member.ID
	}
	w, err := normalizeMaintenanceWindow(MaintenanceWindow{Reason: draft.Reason, Timezone: draft.Timezone, StartsAt: draft.StartsAt, EndsAt: draft.EndsAt, NodeIDs: ids, CreatedBy: "preview"})
	if err != nil {
		return MaintenancePreview{}, err
	}
	draft.Reason, draft.StartsAt, draft.EndsAt = w.Reason, w.StartsAt, w.EndsAt
	// Canonical order without modifying slices owned by the caller.
	draft.Selection.NodeIDs = append([]string(nil), draft.Selection.NodeIDs...)
	draft.Selection.Tags = append([]string(nil), draft.Selection.Tags...)
	sort.Strings(draft.Selection.NodeIDs)
	sort.Strings(draft.Selection.Tags)
	preview := MaintenancePreview{Draft: draft, Members: members}
	content, err := json.Marshal(struct {
		Version int                 `json:"version"`
		Draft   MaintenanceDraft    `json:"draft"`
		Members []MaintenanceMember `json:"members"`
	}{1, draft, members})
	if err != nil {
		return MaintenancePreview{}, err
	}
	sum := sha256.Sum256(content)
	preview.Digest = hex.EncodeToString(sum[:])
	return preview, nil
}
