package alerts

import (
	"errors"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

func classifySMTPFailure(err error, stage string, mayHaveDelivered bool) *DeliveryError {
	result := &DeliveryError{Class: "smtp_" + stage, Ambiguous: mayHaveDelivered}
	var reply *textproto.Error
	if errors.As(err, &reply) {
		// A protocol rejection is definitive; transport loss after DATA is not.
		result.Ambiguous = false
		result.Permanent = reply.Code >= 500 && reply.Code < 600
	}
	return result
}

// DeliveryError carries only safe classifications, never provider bodies or URLs.
type DeliveryError struct {
	Ambiguous  bool
	Class      string
	Permanent  bool
	RetryAfter time.Duration
}

func (e *DeliveryError) Error() string { return e.Class }

func classifyHTTPFailure(status int, retryAfter string, now time.Time) *DeliveryError {
	e := &DeliveryError{Class: "http_error"}
	switch {
	case status == http.StatusTooManyRequests:
		e.Class = "http_rate_limited"
	case status == http.StatusRequestTimeout:
		e.Class = "http_timeout"
	case status >= 500:
		e.Class = "http_server_error"
	case status >= 400:
		e.Class, e.Permanent = "http_client_error", true
	}
	if !e.Permanent {
		value := strings.TrimSpace(retryAfter)
		if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
			// Saturate before converting untrusted seconds to nanoseconds.
			const maxSeconds = uint64((1<<63 - 1) / int64(time.Second))
			if seconds > maxSeconds {
				seconds = maxSeconds
			}
			e.RetryAfter = time.Duration(seconds) * time.Second
		} else if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
			e.RetryAfter = deadline.Sub(now)
		}
	}
	return e
}
