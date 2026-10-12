package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaintenanceSelection describes how members were chosen. Tag selections are
// resolved once when a window is created, unlike dynamic alert policy scopes.
type MaintenanceSelection struct {
	Kind    string   `json:"kind"`
	NodeIDs []string `json:"node_ids,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	TagMode string   `json:"tag_mode,omitempty"`
}

type MaintenanceMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type maintenanceSelectionQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// PreviewMaintenanceMembers is read-only. Creation must resolve again in its
// write transaction and compare with the confirmed members before persisting.
func (s *Store) PreviewMaintenanceMembers(ctx context.Context, selection MaintenanceSelection) ([]MaintenanceMember, error) {
	return resolveMaintenanceMembers(ctx, s.db, selection)
}

func resolveMaintenanceMembers(ctx context.Context, q maintenanceSelectionQuery, selection MaintenanceSelection) ([]MaintenanceMember, error) {
	var values []string
	switch selection.Kind {
	case "nodes":
		if len(selection.Tags) != 0 || selection.TagMode != "" {
			return nil, ErrInvalidMaintenance
		}
		values = selection.NodeIDs
	case "tags":
		if len(selection.NodeIDs) != 0 || (selection.TagMode != "all" && selection.TagMode != "any") {
			return nil, ErrInvalidMaintenance
		}
		values = selection.Tags
	default:
		return nil, ErrInvalidMaintenance
	}
	if len(values) == 0 || len(values) > 100 {
		return nil, ErrInvalidMaintenance
	}
	wanted := make(map[string]bool, len(values))
	for _, value := range values {
		if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 || wanted[value] {
			return nil, ErrInvalidMaintenance
		}
		wanted[value] = true
	}
	query := `SELECT id,name,tags_json FROM nodes`
	args := []any{}
	if selection.Kind == "nodes" {
		query += ` WHERE id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(values)), ",") + `)`
		for _, id := range values {
			args = append(args, id)
		}
	}
	query += ` ORDER BY id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []MaintenanceMember{}
	for rows.Next() {
		var member MaintenanceMember
		var raw string
		if err := rows.Scan(&member.ID, &member.Name, &raw); err != nil {
			return nil, err
		}
		match := selection.Kind == "nodes"
		if !match {
			var tags []string
			if err := json.Unmarshal([]byte(raw), &tags); err != nil {
				return nil, err
			}
			present := make(map[string]bool, len(tags))
			for _, tag := range tags {
				present[tag] = true
			}
			count := 0
			for tag := range wanted {
				if present[tag] {
					count++
				}
			}
			match = (selection.TagMode == "any" && count > 0) || (selection.TagMode == "all" && count == len(wanted))
		}
		if match {
			members = append(members, member)
			if len(members) > 100 {
				return nil, ErrMaintenanceLimit
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(members) == 0 || (selection.Kind == "nodes" && len(members) != len(wanted)) {
		return nil, ErrInvalidMaintenance
	}
	return members, nil
}
