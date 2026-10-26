package alerts

import (
	"io"
	"net/http"
	"net/textproto"
	"testing"
	"time"
)

func TestSMTPFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err                             error
		stage                           string
		afterData, permanent, ambiguous bool
	}{
		{&textproto.Error{Code: 535, Msg: "private credential diagnostic"}, "authentication", false, true, false},
		{&textproto.Error{Code: 454, Msg: "temporary auth failure"}, "authentication", false, false, false},
		{&textproto.Error{Code: 550, Msg: "private recipient"}, "recipient", false, true, false},
		{&textproto.Error{Code: 451, Msg: "retry"}, "delivery", true, false, false},
		{io.ErrUnexpectedEOF, "delivery", true, false, true},
		{io.ErrUnexpectedEOF, "connection", false, false, false},
	} {
		got := classifySMTPFailure(tc.err, tc.stage, tc.afterData)
		if got.Permanent != tc.permanent || got.Ambiguous != tc.ambiguous || got.Error() != "smtp_"+tc.stage {
			t.Fatalf("%+v => %+v", tc, got)
		}
	}
}

func TestHTTPDeliveryFailureClassification(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		status        int
		header, class string
		permanent     bool
		delay         time.Duration
	}{
		{401, "60", "http_client_error", true, 0},
		{403, "", "http_client_error", true, 0},
		{400, "", "http_client_error", true, 0},
		{408, "", "http_timeout", false, 0},
		{429, "120", "http_rate_limited", false, 120 * time.Second},
		{503, now.Add(time.Minute).Format(http.TimeFormat), "http_server_error", false, time.Minute},
		{500, "invalid secret provider response", "http_server_error", false, 0},
		{503, "-1", "http_server_error", false, 0},
		{503, now.Add(-time.Minute).Format(http.TimeFormat), "http_server_error", false, 0},
	} {
		e := classifyHTTPFailure(tc.status, tc.header, now)
		if e.Class != tc.class || e.Permanent != tc.permanent || e.RetryAfter != tc.delay || e.Error() != tc.class {
			t.Errorf("status %d: got %+v", tc.status, e)
		}
	}
	if got := classifyHTTPFailure(429, "18446744073709551615", now).RetryAfter; got <= 0 {
		t.Fatalf("untrusted Retry-After overflowed: %v", got)
	}
}
