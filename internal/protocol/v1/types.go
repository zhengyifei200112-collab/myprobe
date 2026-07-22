package v1

import (
	"encoding/json"
	"time"
)

const (
	Version           = 1
	MaxMessageBytes   = 256 << 10
	TypeHello         = "hello"
	TypeReport        = "report"
	TypePingResult    = "ping_result"
	TypeTCPingResult  = "tcping_result"
	TypeHeartbeat     = "heartbeat"
	TypeWelcome       = "welcome"
	TypeAcknowledged  = "ack"
	TypeConfiguration = "config"
	TypeTask          = "task"
	TypeError         = "error"
	TaskKindPing      = "ping"
	TaskKindTCPing    = "tcping"
)

// Envelope is the stable outer wire shape shared by every protocol message.
type Envelope struct {
	Version  int             `json:"version"`
	Type     string          `json:"type"`
	Sequence uint64          `json:"sequence,omitempty"`
	SentAt   time.Time       `json:"sent_at"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

func NewEnvelope(messageType string, sequence uint64, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{
		Version:  Version,
		Type:     messageType,
		Sequence: sequence,
		SentAt:   time.Now().UTC(),
		Payload:  raw,
	}, nil
}

func DecodePayload[T any](envelope Envelope) (T, error) {
	var value T
	err := json.Unmarshal(envelope.Payload, &value)
	return value, err
}

type Hello struct {
	AgentVersion      string   `json:"agent_version"`
	Hostname          string   `json:"hostname"`
	MachineID         string   `json:"machine_id,omitempty"`
	OS                string   `json:"os"`
	Platform          string   `json:"platform"`
	PlatformVersion   string   `json:"platform_version"`
	KernelVersion     string   `json:"kernel_version"`
	Architecture      string   `json:"architecture"`
	Capabilities      []string `json:"capabilities"`
	CollectionSeconds int      `json:"collection_seconds"`
	ReportSeconds     int      `json:"report_seconds"`
}

type Report struct {
	CapturedAt   time.Time           `json:"captured_at"`
	CPU          CPUMetric           `json:"cpu"`
	Memory       MemoryMetric        `json:"memory"`
	Swap         MemoryMetric        `json:"swap"`
	Disks        []DiskMetric        `json:"disks"`
	Networks     []NetworkMetric     `json:"networks"`
	Load         LoadMetric          `json:"load"`
	Uptime       uint64              `json:"uptime_seconds"`
	Processes    int                 `json:"processes"`
	Temperatures []TemperatureMetric `json:"temperatures,omitempty"`
	PublicIP     string              `json:"public_ip,omitempty"`
}

type CPUMetric struct {
	Model        string  `json:"model"`
	LogicalCores int     `json:"logical_cores"`
	Architecture string  `json:"architecture"`
	UsagePercent float64 `json:"usage_percent"`
}

type MemoryMetric struct {
	TotalBytes   uint64  `json:"total_bytes"`
	UsedBytes    uint64  `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type DiskMetric struct {
	Mount        string  `json:"mount"`
	Filesystem   string  `json:"filesystem,omitempty"`
	TotalBytes   uint64  `json:"total_bytes"`
	UsedBytes    uint64  `json:"used_bytes"`
	UsagePercent float64 `json:"usage_percent"`
}

type NetworkMetric struct {
	Interface    string  `json:"interface"`
	RXTotalBytes uint64  `json:"rx_total_bytes"`
	TXTotalBytes uint64  `json:"tx_total_bytes"`
	RXBytesPerS  float64 `json:"rx_bytes_per_second"`
	TXBytesPerS  float64 `json:"tx_bytes_per_second"`
}

type LoadMetric struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

type TemperatureMetric struct {
	Sensor  string  `json:"sensor"`
	Celsius float64 `json:"celsius"`
}

type LatencyResult struct {
	TaskID      string    `json:"task_id"`
	TargetID    string    `json:"target_id"`
	Success     bool      `json:"success"`
	LatencyMS   float64   `json:"latency_ms,omitempty"`
	ErrorClass  string    `json:"error_class,omitempty"`
	CompletedAt time.Time `json:"completed_at"`
}

type Welcome struct {
	ConnectionID string    `json:"connection_id"`
	ServerTime   time.Time `json:"server_time"`
	Config       Config    `json:"config"`
}

type Acknowledgement struct {
	Sequence uint64 `json:"sequence"`
}

type Config struct {
	CollectionSeconds int      `json:"collection_seconds"`
	ReportSeconds     int      `json:"report_seconds"`
	Interfaces        []string `json:"interfaces,omitempty"`
	Mounts            []string `json:"mounts,omitempty"`
}

type Task struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	TargetID  string    `json:"target_id"`
	Host      string    `json:"host"`
	Port      int       `json:"port,omitempty"`
	TimeoutMS int       `json:"timeout_ms"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
