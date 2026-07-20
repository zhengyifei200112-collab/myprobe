package store

import (
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

type Node struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	SortOrder         int        `json:"sort_order"`
	Hidden            bool       `json:"hidden"`
	Tags              []string   `json:"tags"`
	CountryCode       string     `json:"country_code"`
	Currency          string     `json:"currency"`
	PriceMinor        *int64     `json:"price_minor,omitempty"`
	BillingCycle      string     `json:"billing_cycle"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	TrafficResetDay   *int       `json:"traffic_reset_day,omitempty"`
	UseSinceBoot      bool       `json:"use_since_boot"`
	LatencyMode       string     `json:"latency_mode"`
	CustomHTML        string     `json:"custom_html,omitempty"`
	CollectionSeconds int        `json:"collection_seconds"`
	ReportSeconds     int        `json:"report_seconds"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastSeenAt        *time.Time `json:"last_seen_at,omitempty"`
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
