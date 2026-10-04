package httpprobe

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

var errRedirectLimit = errors.New("HTTP redirect limit")

type Executor struct {
	dialer guardedDialer
	slots  chan struct{}
}

func New(policy Policy) *Executor {
	return &Executor{dialer: guardedDialer{policy: policy}, slots: make(chan struct{}, 4)}
}

func (e *Executor) Execute(ctx context.Context, task httpcheck.Task) (result httpcheck.Result) {
	started := time.Now()
	result = httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, Outcome: "unobserved", ErrorClass: "internal"}
	defer func() {
		result.CompletedAt = time.Now().UTC()
		result.DurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	}()
	if task.Validate(started) != nil {
		result.ErrorClass = "invalid_task"
		return
	}
	if ctx.Err() != nil {
		result.ErrorClass = "cancelled"
		return
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		result.ErrorClass = "busy"
		return
	}
	deadline := started.Add(time.Duration(task.Spec.TimeoutMS) * time.Millisecond)
	if task.ExpiresAt.Before(deadline) {
		deadline = task.ExpiresAt
	}
	requestCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var certMu sync.Mutex
	certificates := make([]httpcheck.Certificate, 0, task.Spec.MaxRedirects+1)
	transport := &http.Transport{
		Proxy: nil, DialContext: e.dialer.DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10,
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) > 0 {
				leaf := state.PeerCertificates[0]
				fingerprint := sha256.Sum256(leaf.Raw)
				certMu.Lock()
				certificates = append(certificates, httpcheck.Certificate{SHA256: hex.EncodeToString(fingerprint[:]), NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, Verified: true})
				certMu.Unlock()
			}
			return nil
		}},
	}
	defer transport.CloseIdleConnections()
	defer func() {
		certMu.Lock()
		result.Certificates = append([]httpcheck.Certificate(nil), certificates...)
		certMu.Unlock()
	}()
	client := &http.Client{Transport: transport, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > task.Spec.MaxRedirects {
			return errRedirectLimit
		}
		check := task.Spec
		check.URL = req.URL.String()
		if check.Validate() != nil {
			return ErrPolicyDenied
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
			return ErrPolicyDenied
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(requestCtx, task.Spec.Method, task.Spec.URL, nil)
	if err != nil {
		result.ErrorClass = "invalid_task"
		return
	}
	response, err := client.Do(request)
	if err != nil {
		result.Outcome, result.ErrorClass = classifyError(ctx, err)
		return
	}
	defer response.Body.Close()
	result.StatusCode = response.StatusCode
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(task.Spec.MaxBodyBytes)+1))
	if err != nil {
		result.Outcome, result.ErrorClass = classifyError(ctx, err)
		if result.Outcome == "unobserved" {
			result.StatusCode = 0
		}
		return
	}
	result.Outcome = "failure"
	if len(body) > task.Spec.MaxBodyBytes {
		result.ErrorClass = "response_too_large"
		return
	}
	allowed := false
	for _, status := range task.Spec.StatusCodes {
		if status == response.StatusCode {
			allowed = true
		}
	}
	if !allowed {
		result.ErrorClass = "status_mismatch"
		return
	}
	if task.Spec.Assertion != nil {
		matched, err := task.Spec.Assertion.Matches(body)
		if err != nil || !matched {
			result.ErrorClass = "content_mismatch"
			return
		}
	}
	result.Outcome, result.ErrorClass = "success", ""
	return
}

func classifyError(ctx context.Context, err error) (string, string) {
	if ctx.Err() != nil {
		return "unobserved", "cancelled"
	}
	if errors.Is(err, ErrPolicyDenied) {
		return "unobserved", "policy_denied"
	}
	if errors.Is(err, errRedirectLimit) {
		return "failure", "redirect_limit"
	}
	var certError x509.CertificateInvalidError
	if errors.As(err, &certError) && certError.Reason == x509.Expired && time.Now().After(certError.Cert.NotAfter) {
		return "failure", "certificate_expired"
	}
	var verification *tls.CertificateVerificationError
	if errors.As(err, &verification) {
		return "failure", "tls_invalid"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "failure", "timeout"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "failure", "dns"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "failure", "refused"
	}
	// No raw URL, address, response body or library error is returned.
	return "unobserved", "internal"
}
