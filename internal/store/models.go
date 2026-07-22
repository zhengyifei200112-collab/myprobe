package store

import (
	"encoding/json"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type User struct {
	ID           string
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Session struct {
	ID        string
	UserID    string
	CSRFToken string
	ExpiresAt time.Time
}

type LoginGuard struct {
	CaptchaRequired bool
	BlockedUntil    *time.Time
}

type AuditEntry struct {
	ID         int64           `json:"id"`
	UserID     *string         `json:"user_id,omitempty"`
	Username   string          `json:"username,omitempty"`
	Action     string          `json:"action"`
	ObjectType string          `json:"object_type"`
	ObjectID   string          `json:"object_id"`
	RemoteIP   string          `json:"remote_ip"`
	Details    json.RawMessage `json:"details"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Node struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	SortOrder         int           `json:"sort_order"`
	Hidden            bool          `json:"hidden"`
	Tags              []string      `json:"tags"`
	CountryCode       string        `json:"country_code"`
	Currency          string        `json:"currency"`
	PriceMinor        *int64        `json:"price_minor,omitempty"`
	BillingCycle      string        `json:"billing_cycle"`
	ExpiresAt         *time.Time    `json:"expires_at,omitempty"`
	TrafficResetDay   *int          `json:"traffic_reset_day,omitempty"`
	UseSinceBoot      bool          `json:"use_since_boot"`
	LatencyMode       string        `json:"latency_mode"`
	CustomHTML        string        `json:"custom_html,omitempty"`
	CustomBadges      []CustomBadge `json:"custom_badges,omitempty"`
	CustomLinks       []CustomLink  `json:"custom_links,omitempty"`
	CollectionSeconds int           `json:"collection_seconds"`
	ReportSeconds     int           `json:"report_seconds"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
	LastSeenAt        *time.Time    `json:"last_seen_at,omitempty"`
}

type CustomBadge struct {
	Label string `json:"label"`
	Color string `json:"color"`
}

type CustomLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type CreateNodeParams struct {
	ID                string
	Name              string
	Tags              []string
	CountryCode       string
	CollectionSeconds int
	ReportSeconds     int
}

type UpdateNodeParams struct {
	Name              string
	SortOrder         int
	Hidden            bool
	Tags              []string
	CountryCode       string
	Currency          string
	PriceMinor        *int64
	BillingCycle      string
	ExpiresAt         *time.Time
	TrafficResetDay   *int
	UseSinceBoot      bool
	LatencyMode       string
	CustomHTML        string
	CustomBadges      []CustomBadge
	CustomLinks       []CustomLink
	CollectionSeconds int
	ReportSeconds     int
}

type PublicNode struct {
	Node    Node             `json:"node"`
	Online  bool             `json:"online"`
	Stale   bool             `json:"stale"`
	Report  *protocol.Report `json:"report,omitempty"`
	Latency []LatestLatency  `json:"latency,omitempty"`
	Traffic TrafficUsage     `json:"traffic"`
}

type TrafficUsage struct {
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	RXBytes     uint64    `json:"rx_bytes"`
	TXBytes     uint64    `json:"tx_bytes"`
}

type TrafficHistoryPoint struct {
	Time    time.Time `json:"time"`
	RXBytes uint64    `json:"rx_bytes"`
	TXBytes uint64    `json:"tx_bytes"`
	Total   uint64    `json:"total_bytes"`
}

type Target struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	Host            string    `json:"host"`
	Port            *int      `json:"port,omitempty"`
	IntervalSeconds int       `json:"interval_seconds"`
	TimeoutMS       int       `json:"timeout_ms"`
	Enabled         bool      `json:"enabled"`
	SortOrder       int       `json:"sort_order"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateTargetParams struct {
	Name            string
	Kind            string
	Host            string
	Port            *int
	IntervalSeconds int
	TimeoutMS       int
}

type UpdateTargetParams struct {
	Name            string
	Kind            string
	Host            string
	Port            *int
	IntervalSeconds int
	TimeoutMS       int
	Enabled         bool
	SortOrder       int
}

type TargetGroup struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TargetAssignment struct {
	NodeID string `json:"node_id"`
	Target Target `json:"target"`
}

type TargetGroupMember struct {
	GroupID  string `json:"group_id"`
	TargetID string `json:"target_id"`
}

type NodeTargetGroup struct {
	NodeID  string `json:"node_id"`
	GroupID string `json:"group_id"`
}

type LatestLatency struct {
	TargetID  string     `json:"target_id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	Success   *bool      `json:"success,omitempty"`
	LatencyMS *float64   `json:"latency_ms,omitempty"`
	Error     string     `json:"error_class,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type MetricHistoryPoint struct {
	Time          time.Time `json:"time"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemoryPercent float64   `json:"memory_percent"`
	DiskPercent   float64   `json:"disk_percent"`
	RXBytesPerS   float64   `json:"rx_bytes_per_second"`
	TXBytesPerS   float64   `json:"tx_bytes_per_second"`
}

type LatencyHistoryPoint struct {
	Time        time.Time `json:"time"`
	TargetID    string    `json:"target_id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	LatencyMS   *float64  `json:"latency_ms,omitempty"`
	SuccessRate float64   `json:"success_rate"`
}

type NotificationChannel struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	ConfigEncrypted string    `json:"-"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type AlertRule struct {
	ID              string          `json:"id"`
	NodeID          string          `json:"node_id"`
	ChannelID       string          `json:"channel_id"`
	Kind            string          `json:"kind"`
	Config          json.RawMessage `json:"config"`
	Enabled         bool            `json:"enabled"`
	CooldownSeconds int             `json:"cooldown_seconds"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AlertState struct {
	Fingerprint     string
	RuleID          string
	NodeID          string
	Active          bool
	LastMessage     string
	LastAttemptAt   time.Time
	LastDeliveredAt *time.Time
	LastError       string
	UpdatedAt       time.Time
}

type AlertEvent struct {
	ID            string     `json:"id"`
	RuleID        string     `json:"rule_id"`
	NodeID        string     `json:"node_id,omitempty"`
	State         string     `json:"state"`
	Fingerprint   string     `json:"fingerprint"`
	Message       string     `json:"message"`
	DeliveryError string     `json:"delivery_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
}

type ChartShare struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	NodeIDs      []string  `json:"node_ids"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ChartShareSession struct {
	ID        string
	ShareID   string
	ExpiresAt time.Time
}
