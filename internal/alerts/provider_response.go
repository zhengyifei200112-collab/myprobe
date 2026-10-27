package alerts

import (
	"encoding/json"
	"io"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const providerResponseByteLimit = 64 << 10

var providerDelayNumber = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func (s *HTTPSender) doConfirmed(request *http.Request, provider string) error {
	response, err := s.client.Do(request)
	if err != nil {
		return &DeliveryError{Class: "transport_error", Ambiguous: true}
	}
	defer response.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, providerResponseByteLimit+1))
	validBody := readErr == nil && len(raw) <= providerResponseByteLimit
	var telegram struct {
		OK         *bool `json:"ok"`
		ErrorCode  int   `json:"error_code"`
		Parameters struct {
			RetryAfter uint64 `json:"retry_after"`
		} `json:"parameters"`
		Result struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"result"`
	}
	if provider == "telegram" {
		validBody = validBody && json.Unmarshal(raw, &telegram) == nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		outcome := classifyHTTPFailure(response.StatusCode, response.Header.Get("Retry-After"), time.Now())
		if provider == "discord" && !outcome.Permanent {
			delay := providerRetryDelay(response.Header.Get("Retry-After"))
			var rate struct {
				RetryAfter json.Number `json:"retry_after"`
			}
			if validBody && json.Unmarshal(raw, &rate) == nil {
				if bodyDelay := providerRetryDelay(string(rate.RetryAfter)); bodyDelay > delay {
					delay = bodyDelay
				}
			}
			if delay > outcome.RetryAfter {
				outcome.RetryAfter = delay
			}
		}
		if provider == "telegram" && validBody && !outcome.Permanent {
			delay := classifyHTTPFailure(429, strconv.FormatUint(telegram.Parameters.RetryAfter, 10), time.Now()).RetryAfter
			if delay > outcome.RetryAfter {
				outcome.RetryAfter = delay
			}
		}
		return outcome
	}
	invalid := &DeliveryError{Class: provider + "_invalid_response", Ambiguous: true}
	if !validBody {
		return invalid
	}
	if provider == "telegram" {
		if telegram.OK == nil {
			return invalid
		}
		if !*telegram.OK {
			if telegram.ErrorCode < 400 || telegram.ErrorCode > 599 {
				return &DeliveryError{Class: "telegram_rejected"}
			}
			result := classifyHTTPFailure(telegram.ErrorCode, response.Header.Get("Retry-After"), time.Now())
			bodyDelay := classifyHTTPFailure(telegram.ErrorCode, strconv.FormatUint(telegram.Parameters.RetryAfter, 10), time.Now()).RetryAfter
			if bodyDelay > result.RetryAfter {
				result.RetryAfter = bodyDelay
			}
			return result
		}
		if telegram.Result.MessageID <= 0 || telegram.Result.Chat.ID == 0 {
			return invalid
		}
		return nil
	}
	var message struct {
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
	}
	if json.Unmarshal(raw, &message) != nil || !validSnowflake(message.ID) || !validSnowflake(message.ChannelID) {
		return invalid
	}
	return nil
}

// Discord reports fractional seconds. Round the decimal upward before storing
// a deadline; saturate untrusted magnitudes without overflowing durations.
func providerRetryDelay(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if !providerDelayNumber.MatchString(raw) {
		return 0
	}
	value, err := strconv.ParseFloat(raw, 64)
	if math.IsInf(value, 1) {
		return time.Duration(math.MaxInt64)
	}
	if err != nil || value <= 0 || math.IsNaN(value) {
		return 0
	}
	if value >= float64(math.MaxInt64)/float64(time.Second) {
		return time.Duration(math.MaxInt64)
	}
	if len(raw) <= 128 {
		if rational, ok := new(big.Rat).SetString(raw); ok {
			rational.Mul(rational, new(big.Rat).SetInt64(int64(time.Second)))
			quotient, remainder := new(big.Int), new(big.Int)
			quotient.QuoRem(rational.Num(), rational.Denom(), remainder)
			if remainder.Sign() > 0 {
				quotient.Add(quotient, big.NewInt(1))
			}
			if !quotient.IsInt64() {
				return time.Duration(math.MaxInt64)
			}
			return time.Duration(quotient.Int64())
		}
	}
	// Extremely long numeric spellings use a conservative millisecond margin.
	nanos := math.Ceil(value*float64(time.Second)) + float64(time.Millisecond)
	if nanos >= float64(math.MaxInt64) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(nanos)
}
func validSnowflake(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return err == nil && n > 0
}
