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

type SiteLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type BackgroundSettings struct {
	URL      string  `json:"url"`
	Fit      string  `json:"fit"`
	Position string  `json:"position"`
	Blur     int     `json:"blur"`
	Opacity  float64 `json:"opacity"`
	Overlay  float64 `json:"overlay"`
}

// SiteSettings contains public presentation settings and the address copied into
// Agent installation commands. AgentURL is empty when the browser origin is used.
type SiteSettings struct {
	AgentURL             string             `json:"agent_url"`
	SiteName             string             `json:"site_name"`
	SiteSummary          string             `json:"site_summary"`
	BrowserTitle         string             `json:"browser_title"`
	SiteTitle            string             `json:"site_title"`
	SiteDescription      string             `json:"site_description"`
	DashboardTitle       string             `json:"dashboard_title"`
	DashboardDescription string             `json:"dashboard_description"`
	LogoURL              string             `json:"logo_url"`
	FaviconURL           string             `json:"favicon_url"`
	FooterText           string             `json:"footer_text"`
	CopyrightText        string             `json:"copyright_text"`
	GitHubURL            string             `json:"github_url"`
	BlogURL              string             `json:"blog_url"`
	Contact              string             `json:"contact"`
	CustomLinks          []SiteLink         `json:"custom_links"`
	ThemeMode            string             `json:"theme_mode"`
	AccentColor          string             `json:"accent_color"`
	PublicBackground     BackgroundSettings `json:"public_background"`
	AdminBackground      BackgroundSettings `json:"admin_background"`
	LoginBackground      BackgroundSettings `json:"login_background"`
	HeaderHTML           string             `json:"header_html"`
	FooterHTML           string             `json:"footer_html"`
}

func DefaultSiteSettings() SiteSettings {
	background := BackgroundSettings{Fit: "cover", Position: "center", Opacity: 1, Overlay: 0.12}
	return SiteSettings{
		SiteName: "MyProbe", BrowserTitle: "MyProbe · 服务器探针",
		SiteTitle: "服务器运行概览", SiteDescription: "节点状态、资源占用、实时速率与网络延迟集中展示。",
		DashboardTitle: "服务器运行概览", DashboardDescription: "节点状态、资源占用、实时速率与网络延迟集中展示。",
		ThemeMode: "system", AccentColor: "blue", CustomLinks: []SiteLink{},
		PublicBackground: background, AdminBackground: background, LoginBackground: background,
	}
}

func (s *Store) GetSiteSettings(ctx context.Context) (SiteSettings, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key=?`, siteSettingsKey).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSiteSettings(), nil
	}
	if err != nil {
		return SiteSettings{}, err
	}
	settings := DefaultSiteSettings()
	if err := json.Unmarshal([]byte(encoded), &settings); err != nil {
		return SiteSettings{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(encoded), &fields); err != nil {
		return SiteSettings{}, err
	}
	if _, exists := fields["dashboard_title"]; !exists && settings.SiteTitle != "" {
		settings.DashboardTitle = settings.SiteTitle
	}
	if _, exists := fields["dashboard_description"]; !exists && settings.SiteDescription != "" {
		settings.DashboardDescription = settings.SiteDescription
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
	settings.SiteName = strings.TrimSpace(settings.SiteName)
	if settings.SiteName == "" {
		settings.SiteName = "MyProbe"
	}
	settings.SiteSummary = strings.TrimSpace(settings.SiteSummary)
	settings.BrowserTitle = strings.TrimSpace(settings.BrowserTitle)
	if settings.BrowserTitle == "" {
		settings.BrowserTitle = settings.SiteName + " · 服务器探针"
	}
	settings.DashboardTitle = strings.TrimSpace(settings.DashboardTitle)
	if settings.DashboardTitle == "" {
		settings.DashboardTitle = strings.TrimSpace(settings.SiteTitle)
	}
	if settings.DashboardTitle == "" {
		settings.DashboardTitle = "服务器运行概览"
	}
	settings.DashboardDescription = strings.TrimSpace(settings.DashboardDescription)
	if settings.DashboardDescription == "" {
		settings.DashboardDescription = strings.TrimSpace(settings.SiteDescription)
	}
	if settings.DashboardDescription == "" {
		settings.DashboardDescription = "节点状态、资源占用、实时速率与网络延迟集中展示。"
	}
	settings.SiteTitle = settings.DashboardTitle
	settings.SiteDescription = settings.DashboardDescription
	settings.FooterText = strings.TrimSpace(settings.FooterText)
	settings.CopyrightText = strings.TrimSpace(settings.CopyrightText)
	settings.Contact = strings.TrimSpace(settings.Contact)
	if len(settings.SiteName) > 80 || len(settings.SiteSummary) > 300 || len(settings.BrowserTitle) > 120 || len(settings.DashboardTitle) > 80 || len(settings.DashboardDescription) > 300 || len(settings.FooterText) > 300 || len(settings.CopyrightText) > 160 || len(settings.Contact) > 200 {
		return SiteSettings{}, errors.New("site text setting is too long")
	}
	for field, value := range map[string]*string{"logo URL": &settings.LogoURL, "favicon URL": &settings.FaviconURL, "GitHub URL": &settings.GitHubURL, "blog URL": &settings.BlogURL} {
		*value, err = normalizePublicURL(*value, field)
		if err != nil {
			return SiteSettings{}, err
		}
	}
	if len(settings.CustomLinks) > 8 {
		return SiteSettings{}, errors.New("custom links cannot exceed 8 items")
	}
	for index := range settings.CustomLinks {
		settings.CustomLinks[index].Label = strings.TrimSpace(settings.CustomLinks[index].Label)
		if settings.CustomLinks[index].Label == "" || len(settings.CustomLinks[index].Label) > 60 {
			return SiteSettings{}, errors.New("custom link label is required and cannot exceed 60 characters")
		}
		settings.CustomLinks[index].URL, err = normalizePublicURL(settings.CustomLinks[index].URL, "custom link URL")
		if err != nil || settings.CustomLinks[index].URL == "" {
			return SiteSettings{}, errors.New("custom link must use a valid HTTP(S) URL")
		}
	}
	if settings.ThemeMode == "" {
		settings.ThemeMode = "system"
	}
	if settings.ThemeMode != "light" && settings.ThemeMode != "dark" && settings.ThemeMode != "system" {
		return SiteSettings{}, errors.New("theme mode must be light, dark, or system")
	}
	if settings.AccentColor == "" {
		settings.AccentColor = "blue"
	}
	if !containsSetting([]string{"blue", "purple", "green", "orange", "pink"}, settings.AccentColor) {
		return SiteSettings{}, errors.New("accent color is not supported")
	}
	for field, background := range map[string]*BackgroundSettings{"public background": &settings.PublicBackground, "admin background": &settings.AdminBackground, "login background": &settings.LoginBackground} {
		if err := normalizeBackground(background, field); err != nil {
			return SiteSettings{}, err
		}
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

func normalizePublicURL(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 {
		return "", errors.New(field + " is too long")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return "", errors.New(field + " must be a valid HTTP(S) URL without credentials")
	}
	return value, nil
}

func normalizeBackground(background *BackgroundSettings, field string) error {
	var err error
	background.URL, err = normalizePublicURL(background.URL, field+" URL")
	if err != nil {
		return err
	}
	if background.Fit == "" {
		background.Fit = "cover"
	}
	if background.Position == "" {
		background.Position = "center"
	}
	if !containsSetting([]string{"cover", "contain", "original"}, background.Fit) {
		return errors.New(field + " fit is not supported")
	}
	if !containsSetting([]string{"center", "top", "bottom"}, background.Position) {
		return errors.New(field + " position is not supported")
	}
	if background.Blur < 0 || background.Blur > 24 || background.Opacity < 0 || background.Opacity > 1 || background.Overlay < 0 || background.Overlay > 0.8 {
		return errors.New(field + " visual values are outside the allowed range")
	}
	if background.URL == "" && background.Opacity == 0 {
		background.Opacity = 1
	}
	return nil
}

func containsSetting(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
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
