package httpcheck

import (
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Task struct {
	ID          string    `json:"id"`
	ServiceID   string    `json:"service_id"`
	Revision    uint64    `json:"revision"`
	ScheduledAt time.Time `json:"scheduled_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Spec        Spec      `json:"spec"`
}

func validID(value string) bool {
	return len(value) > 0 && len(value) <= 128 && utf8.ValidString(value) && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func (t Task) Validate(now time.Time) error {
	if !validID(t.ID) || !validID(t.ServiceID) || t.Revision == 0 {
		return errors.New("invalid HTTP task identity")
	}
	if t.ScheduledAt.IsZero() || t.ScheduledAt.Before(now.Add(-10*time.Minute)) || t.ScheduledAt.After(now.Add(time.Minute)) || !t.ExpiresAt.After(now) || t.ExpiresAt.After(now.Add(10*time.Minute)) || !t.ExpiresAt.After(t.ScheduledAt) {
		return errors.New("invalid HTTP task time window")
	}
	return t.Spec.Validate()
}

type Result struct {
	TaskID       string        `json:"task_id"`
	ServiceID    string        `json:"service_id"`
	Revision     uint64        `json:"revision"`
	ScheduledAt  time.Time     `json:"scheduled_at"`
	CompletedAt  time.Time     `json:"completed_at"`
	Outcome      string        `json:"outcome"`
	ErrorClass   string        `json:"error_class,omitempty"`
	StatusCode   int           `json:"status_code,omitempty"`
	DurationMS   float64       `json:"duration_ms"`
	Certificates []Certificate `json:"certificates,omitempty"`
}

// Certificate carries leaf-certificate evidence without hostnames, subjects,
// SANs or the certificate body. Entries follow TLS handshake order.
type Certificate struct {
	SHA256    string    `json:"sha256"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	Verified  bool      `json:"verified"`
}

// ValidateFor checks a result against the Server's original task, not a task
// supplied by the Agent. Session identity and exactly-once storage are separate
// ingestion requirements. A bounded receipt grace permits delayed transport.
func (r Result) ValidateFor(task Task, now time.Time) error {
	if r.TaskID != task.ID || r.ServiceID != task.ServiceID || r.Revision != task.Revision || !r.ScheduledAt.Equal(task.ScheduledAt) || !validID(r.TaskID) || !validID(r.ServiceID) || r.Revision == 0 {
		return errors.New("HTTP result does not match task")
	}
	if r.CompletedAt.IsZero() || r.CompletedAt.Before(task.ScheduledAt) || r.CompletedAt.After(task.ExpiresAt) || r.CompletedAt.After(now.Add(time.Minute)) || now.After(task.ExpiresAt.Add(time.Minute)) {
		return errors.New("HTTP result outside task window")
	}
	if math.IsNaN(r.DurationMS) || math.IsInf(r.DurationMS, 0) || r.DurationMS < 0 || r.DurationMS > float64(task.Spec.TimeoutMS) {
		return errors.New("invalid HTTP result duration")
	}
	if r.StatusCode != 0 && (r.StatusCode < 100 || r.StatusCode > 599) {
		return errors.New("invalid HTTP result status")
	}
	allowed := false
	for _, code := range task.Spec.StatusCodes {
		if r.StatusCode == code {
			allowed = true
		}
	}
	if len(r.Certificates) > task.Spec.MaxRedirects+1 {
		return errors.New("too many HTTP certificate observations")
	}
	for _, certificate := range r.Certificates {
		fingerprint, err := hex.DecodeString(certificate.SHA256)
		if err != nil || len(fingerprint) != 32 || certificate.NotBefore.IsZero() || !certificate.NotAfter.After(certificate.NotBefore) {
			return errors.New("invalid HTTP certificate observation")
		}
		if r.Outcome == "success" && !certificate.Verified {
			return errors.New("successful HTTP check has unverified certificate")
		}
	}
	switch r.Outcome {
	case "success":
		if strings.HasPrefix(task.Spec.URL, "https:") && len(r.Certificates) == 0 {
			return errors.New("successful HTTPS check requires certificate evidence")
		}
		if !allowed || r.ErrorClass != "" {
			return errors.New("inconsistent successful HTTP result")
		}
	case "failure":
		switch r.ErrorClass {
		case "dns", "refused", "timeout", "tls_invalid", "certificate_expired", "status_mismatch", "content_mismatch", "response_too_large", "redirect_limit":
		default:
			return errors.New("invalid HTTP failure class")
		}
		if (r.ErrorClass == "status_mismatch" || r.ErrorClass == "content_mismatch") && r.StatusCode == 0 {
			return errors.New("HTTP response failure requires status")
		}
		if r.ErrorClass == "status_mismatch" && allowed {
			return errors.New("status mismatch uses an allowed status")
		}
		if r.ErrorClass == "content_mismatch" && (task.Spec.Assertion == nil || !allowed) {
			return errors.New("content mismatch requires an assertion and allowed status")
		}
	case "unobserved":
		switch r.ErrorClass {
		case "unsupported", "busy", "cancelled", "invalid_task", "policy_denied", "internal":
		default:
			return errors.New("invalid HTTP unobserved class")
		}
		if r.StatusCode != 0 {
			return errors.New("unobserved check cannot report HTTP status")
		}
	default:
		return errors.New("invalid HTTP result outcome")
	}
	return nil
}
