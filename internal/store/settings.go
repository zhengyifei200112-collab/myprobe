package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/zhengyifei200112-collab/myprobe/internal/sanitize"
)

const siteSettingsKey = "site"

// SiteSettings contains public presentation settings and the address copied into
// Agent installation commands. AgentURL is empty when the browser origin is used.
type SiteSettings struct {
	AgentURL        string `json:"agent_url"`
	SiteTitle       string `json:"site_title"`
	SiteDescription string `json:"site_description"`
	HeaderHTML      string `json:"header_html"`
	FooterHTML      string `json:"footer_html"`
}

func (s *Store) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, siteSettingsKey).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteSettings{}, nil
	}
	if err != nil {
		return SiteSettings{}, err
	}
	var settings SiteSettings
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return SiteSettings{}, err
	}
	return settings, nil
}

func (s *Store) UpdateSiteSettings(ctx context.Context, settings SiteSettings) (SiteSettings, error) {
	settings, err := normalizeSiteSettings(settings)
	if err != nil {
		return SiteSettings{}, err
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return SiteSettings{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings(key,value_json,updated_at) VALUES(?,?,?)
		ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json,updated_at=excluded.updated_at`, siteSettingsKey, string(encoded), nowText())
	return settings, err
}

func normalizeSiteSettings(settings SiteSettings) (SiteSettings, error) {
	var err error
	settings.AgentURL, err = normalizeAgentURL(settings.AgentURL)
	if err != nil {
		return SiteSettings{}, err
	}
	settings.SiteTitle = strings.TrimSpace(settings.SiteTitle)
	settings.SiteDescription = strings.TrimSpace(settings.SiteDescription)
	if len(settings.SiteTitle) > 80 || len(settings.SiteDescription) > 300 {
		return SiteSettings{}, errors.New("site title or description is too long")
	}
	settings.HeaderHTML, err = sanitize.HTML(settings.HeaderHTML)
	if err != nil {
		return SiteSettings{}, err
	}
	settings.FooterHTML, err = sanitize.HTML(settings.FooterHTML)
	if err != nil {
		return SiteSettings{}, err
	}
	return settings, nil
}

func normalizeAgentURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 {
		return "", errors.New("agent connection address is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("agent connection address must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return value, nil
}
