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

type PublicNode struct {
	Node   Node             `json:"node"`
	Online bool             `json:"online"`
	Stale  bool             `json:"stale"`
	Report *protocol.Report `json:"report,omitempty"`
}
