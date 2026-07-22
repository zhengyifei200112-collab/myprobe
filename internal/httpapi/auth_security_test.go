package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type loginSecurityBody struct {
	CSRFToken       string                      `json:"csrf_token"`
	CaptchaRequired bool                        `json:"captcha_required"`
	Captcha         struct{ ID, Prompt string } `json:"captcha"`
}

func TestLoginCaptchaAndPersistentThrottle(t *testing.T) {
	handler, database, databasePath := securityTestServer(t)
	for attempt := 1; attempt <= 3; attempt++ {
		response, body := loginSecurityRequest(t, handler, "admin", "wrong-password", "", "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d %s", attempt, response.Code, response.Body.String())
		}
		if attempt < 3 && body.CaptchaRequired {
			t.Fatalf("CAPTCHA required too early on attempt %d", attempt)
		}
		if attempt == 3 && (!body.CaptchaRequired || body.Captcha.ID == "") {
			t.Fatalf("third attempt body = %#v", body)
		}
	}
	response, challenge := loginSecurityRequest(t, handler, "admin", "wrong-password", "", "")
	if response.Code != http.StatusUnauthorized || challenge.Captcha.ID == "" {
		t.Fatalf("missing CAPTCHA = %d %s", response.Code, response.Body.String())
	}
	response, challenge = loginSecurityRequest(t, handler, "admin", "wrong-password", challenge.Captcha.ID, "999")
	if response.Code != http.StatusUnauthorized || challenge.Captcha.ID == "" {
		t.Fatalf("wrong CAPTCHA = %d %s", response.Code, response.Body.String())
	}
	answer := captchaAnswer(t, challenge.Captcha.Prompt)
	response, _ = loginSecurityRequest(t, handler, "admin", "correct-password", challenge.Captcha.ID, answer)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked login = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Retry-After") == "" {
		t.Fatal("blocked response has no Retry-After")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	authService := auth.New(reopened, time.Hour)
	hub := agentgateway.NewHub()
	restarted := New(config.Config{}, reopened, authService, agentgateway.New(reopened, hub), hub).Handler()
	response, _ = loginSecurityRequest(t, restarted, "admin", "correct-password", "", "")
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("restart blocked login = %d %s", response.Code, response.Body.String())
	}
}

func TestCaptchaSuccessClearsFailuresAndPasswordChangeRevokesSessions(t *testing.T) {
	handler, database, _ := securityTestServer(t)
	defer database.Close()
	var challenge loginSecurityBody
	for attempt := 1; attempt <= 3; attempt++ {
		_, challenge = loginSecurityRequest(t, handler, "admin", "wrong-password", "", "")
	}
	answer := captchaAnswer(t, challenge.Captcha.Prompt)
	response, login := loginSecurityRequest(t, handler, "admin", "correct-password", challenge.Captcha.ID, answer)
	if response.Code != http.StatusOK {
		t.Fatalf("captcha login = %d %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	change := authenticatedRequest(t, handler, cookie, login.CSRFToken, http.MethodPost, "/api/v1/auth/password", `{"current_password":"correct-password","new_password":"new-correct-password"}`)
	if change.Code != http.StatusNoContent {
		t.Fatalf("password change = %d %s", change.Code, change.Body.String())
	}
	me := authenticatedRequest(t, handler, cookie, login.CSRFToken, http.MethodGet, "/api/v1/auth/me", "")
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("old session remained valid: %d", me.Code)
	}
	oldLogin, _ := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("old password login = %d", oldLogin.Code)
	}
	newLogin, newLoginBody := loginSecurityRequest(t, handler, "admin", "new-correct-password", "", "")
	if newLogin.Code != http.StatusOK {
		t.Fatalf("new password login = %d %s", newLogin.Code, newLogin.Body.String())
	}
	auditResponse := authenticatedRequest(t, handler, newLogin.Result().Cookies()[0], newLoginBody.CSRFToken, http.MethodGet, "/api/v1/admin/audit?limit=10", "")
	if auditResponse.Code != http.StatusOK || !bytes.Contains(auditResponse.Body.Bytes(), []byte(`"change_password"`)) {
		t.Fatalf("audit API = %d %s", auditResponse.Code, auditResponse.Body.String())
	}
	entries, err := database.ListAudit(context.Background(), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range entries {
		if item.Action == "change_password" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit entries = %#v", entries)
	}
}

func TestLoginThrottleIgnoresUntrustedForwardedFor(t *testing.T) {
	handler, database, _ := securityTestServer(t)
	defer database.Close()
	var body loginSecurityBody
	for attempt := 1; attempt <= 3; attempt++ {
		_, body = loginSecurityRequestFrom(t, handler, "admin", "wrong-password", "", "", fmt.Sprintf("198.51.100.%d", attempt))
	}
	if !body.CaptchaRequired {
		t.Fatalf("spoofed forwarded addresses bypassed throttle: %#v", body)
	}
}

func securityTestServer(t *testing.T) (http.Handler, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "security.db")
	database, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(database, time.Hour)
	if _, err := authService.Bootstrap(context.Background(), "admin", "correct-password"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	return New(config.Config{}, database, authService, agentgateway.New(database, hub), hub).Handler(), database, path
}

func loginSecurityRequest(t *testing.T, handler http.Handler, username, password, captchaID, captchaAnswer string) (*httptest.ResponseRecorder, loginSecurityBody) {
	return loginSecurityRequestFrom(t, handler, username, password, captchaID, captchaAnswer, "")
}

func loginSecurityRequestFrom(t *testing.T, handler http.Handler, username, password, captchaID, captchaAnswer, forwardedFor string) (*httptest.ResponseRecorder, loginSecurityBody) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"username": username, "password": password, "captcha_id": captchaID, "captcha_answer": captchaAnswer})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if forwardedFor != "" {
		request.Header.Set("X-Forwarded-For", forwardedFor)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body loginSecurityBody
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response, body
}

func captchaAnswer(t *testing.T, prompt string) string {
	t.Helper()
	var left, right int
	if _, err := fmt.Sscanf(prompt, "%d + %d = ?", &left, &right); err != nil {
		t.Fatalf("CAPTCHA prompt %q: %v", prompt, err)
	}
	return fmt.Sprintf("%d", left+right)
}
