package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	backuparchive "github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type delayedMaintenanceResponse struct {
	http.ResponseWriter
	once sync.Once
}

type maintenanceDeadlineRecorder struct {
	*httptest.ResponseRecorder
	reads, writes         []time.Time
	readError, writeError error
}

func (w *maintenanceDeadlineRecorder) SetReadDeadline(at time.Time) error {
	w.reads = append(w.reads, at)
	return w.readError
}

func (w *maintenanceDeadlineRecorder) SetWriteDeadline(at time.Time) error {
	w.writes = append(w.writes, at)
	return w.writeError
}

func TestMaintenanceDeadlineScopeAuthorizationAndParentBudget(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal("login", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	for _, scenario := range []struct {
		name, path                  string
		authenticated, csrf, parent bool
		readError, writeError       error
		status, reads, writes       int
	}{
		{name: "anonymous restore", path: "/api/v1/admin/maintenance/restore", status: 401},
		{name: "missing csrf", path: "/api/v1/admin/maintenance/restore", authenticated: true, status: 403},
		{name: "ordinary admin read", path: "/api/v1/admin/nodes", authenticated: true, csrf: true, status: 200},
		{name: "authorized upload", path: "/api/v1/admin/maintenance/restore", authenticated: true, csrf: true, status: 400, reads: 1, writes: 1},
		{name: "authorized backup", path: "/api/v1/admin/maintenance/backup", authenticated: true, csrf: true, status: 400, writes: 1},
		{name: "shorter parent", path: "/api/v1/admin/maintenance/restore", authenticated: true, csrf: true, parent: true, status: 400, reads: 1, writes: 1},
		{name: "read deadline fails", path: "/api/v1/admin/maintenance/restore", authenticated: true, csrf: true, readError: errors.New("private deadline diagnostic"), status: 500, reads: 1},
		{name: "write deadline fails", path: "/api/v1/admin/maintenance/backup", authenticated: true, csrf: true, writeError: errors.New("private deadline diagnostic"), status: 500, writes: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := &maintenanceDeadlineRecorder{ResponseRecorder: httptest.NewRecorder(), readError: scenario.readError, writeError: scenario.writeError}
			method := http.MethodPost
			if scenario.path == "/api/v1/admin/nodes" {
				method = http.MethodGet
			}
			request := httptest.NewRequest(method, scenario.path, bytes.NewBufferString(`{"passphrase":"short"}`))
			request.Header.Set("Content-Type", "application/json")
			if scenario.authenticated {
				request.AddCookie(cookie)
			}
			if scenario.csrf {
				request.Header.Set("X-CSRF-Token", credentials.CSRFToken)
			}
			parentDeadline := time.Now().Add(2 * time.Second)
			if scenario.parent {
				ctx, cancel := context.WithDeadline(request.Context(), parentDeadline)
				defer cancel()
				request = request.WithContext(ctx)
			}
			started := time.Now()
			handler.ServeHTTP(response, request)
			if response.Code != scenario.status || len(response.reads) != scenario.reads || len(response.writes) != scenario.writes {
				t.Fatalf("status=%d reads=%d writes=%d", response.Code, len(response.reads), len(response.writes))
			}
			if bytes.Contains(response.Body.Bytes(), []byte("private deadline diagnostic")) {
				t.Fatal("transport diagnostic leaked")
			}
			for _, deadline := range append(response.reads, response.writes...) {
				if scenario.parent {
					if !deadline.Equal(parentDeadline) {
						t.Fatal("maintenance extended an earlier parent deadline")
					}
				} else if remaining := deadline.Sub(started); remaining < 14*time.Minute || remaining > 16*time.Minute {
					t.Fatal("maintenance has no bounded15-minute deadline", remaining)
				}
			}
		})
	}
}

func (w *delayedMaintenanceResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *delayedMaintenanceResponse) Write(p []byte) (int, error) {
	w.once.Do(func() { time.Sleep(200 * time.Millisecond) })
	return w.ResponseWriter.Write(p)
}

type cancellingArchiveSource struct {
	reader *bytes.Reader
	cancel context.CancelFunc
}

func (r *cancellingArchiveSource) Read(p []byte) (int, error) {
	// Cancel between source reads even when the fixture fits in one crypto chunk.
	if len(p) > 4096 {
		p = p[:4096]
	}
	n, err := r.reader.Read(p)
	r.cancel()
	return n, err
}

func TestMaintenanceArchiveCancellationStopsCrypto(t *testing.T) {
	plain := bytes.Repeat([]byte("synthetic archive block"), 8192)
	var encrypted bytes.Buffer
	if err := backuparchive.Encrypt(&encrypted, bytes.NewReader(plain), "synthetic cancel passphrase"); err != nil {
		t.Fatal(err)
	}
	for _, decrypt := range []bool{false, true} {
		t.Run(fmt.Sprintf("decrypt=%t", decrypt), func(t *testing.T) {
			input := plain
			if decrypt {
				input = encrypted.Bytes()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &cancellingArchiveSource{reader: bytes.NewReader(input), cancel: cancel}
			reader := maintenanceArchiveReader{ctx: ctx, reader: source}
			var output bytes.Buffer
			var err error
			if decrypt {
				err = backuparchive.Decrypt(&output, reader, "synthetic cancel passphrase")
			} else {
				err = backuparchive.Encrypt(&output, reader, "synthetic cancel passphrase")
			}
			if err == nil || source.reader.Len() == 0 {
				t.Fatal("canceled operation consumed the complete archive")
			}
		})
	}
}

// Small real HTTP requests cross shortened ordinary socket deadlines. This
// reproduces a completed restore losing its202 without a large database fixture.
func TestMaintenanceTransfersOutliveOrdinaryHTTPDeadlines(t *testing.T) {
	for _, scenario := range []string{"backup_response", "restore_response", "restore_upload"} {
		t.Run(scenario, func(t *testing.T) {
			databasePath := filepath.Join(t.TempDir(), "deadline.db")
			db, err := store.Open(context.Background(), databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			a := auth.New(db, time.Hour)
			if _, err := a.Bootstrap(context.Background(), "admin", "maintenance-password"); err != nil {
				t.Fatal(err)
			}
			hub := agentgateway.NewHub()
			api := New(config.Config{}, db, a, agentgateway.New(db, hub), hub)
			login := httptest.NewRecorder()
			loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"maintenance-password"}`))
			loginRequest.Header.Set("Content-Type", "application/json")
			api.Handler().ServeHTTP(login, loginRequest)
			if login.Code != 200 {
				t.Fatal("login", login.Code)
			}
			var credentials struct {
				CSRF string `json:"csrf_token"`
			}
			if err := json.Unmarshal(login.Body.Bytes(), &credentials); err != nil {
				t.Fatal(err)
			}
			cookie := login.Result().Cookies()[0]
			requestPath := "/api/v1/admin/maintenance/backup"
			body := []byte(`{"passphrase":"synthetic deadline passphrase"}`)
			contentType := "application/json"
			want := http.StatusOK
			if scenario != "backup_response" {
				archive := authenticatedRequest(t, api.Handler(), cookie, credentials.CSRF, http.MethodPost, requestPath, string(body))
				if archive.Code != 200 {
					t.Fatal("prepare encrypted fixture", archive.Code)
				}
				var multipartBody bytes.Buffer
				writer := multipart.NewWriter(&multipartBody)
				if err := writer.WriteField("passphrase", "synthetic deadline passphrase"); err != nil {
					t.Fatal(err)
				}
				part, err := writer.CreateFormFile("file", "fixture.mpb")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write(archive.Body.Bytes()); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				body, contentType, requestPath, want = multipartBody.Bytes(), writer.FormDataContentType(), "/api/v1/admin/maintenance/restore", http.StatusAccepted
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario != "restore_upload" {
					w = &delayedMaintenanceResponse{ResponseWriter: w}
				}
				api.Handler().ServeHTTP(w, r)
			})
			host := httptest.NewUnstartedServer(handler)
			host.Config.ReadTimeout = 100 * time.Millisecond
			host.Config.WriteTimeout = 100 * time.Millisecond
			host.Start()
			defer host.Close()
			var response *http.Response
			if scenario == "restore_upload" {
				connection, err := net.DialTimeout("tcp", host.Listener.Addr().String(), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
				if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					t.Fatal(err)
				}
				_, err = fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: localhost\r\nContent-Type: %s\r\nContent-Length: %d\r\nCookie: %s\r\nX-CSRF-Token: %s\r\nConnection: close\r\n\r\n", requestPath, contentType, len(body), cookie.String(), credentials.CSRF)
				if err != nil {
					t.Fatal(err)
				}
				time.Sleep(200 * time.Millisecond)
				if _, err := connection.Write(body); err != nil {
					t.Fatal(err)
				}
				response, err = http.ReadResponse(bufio.NewReader(connection), nil)
				if err != nil {
					t.Fatalf("slow authorized upload lost response: %v", err)
				}
			} else {
				request, err := http.NewRequest(http.MethodPost, host.URL+requestPath, bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				request.AddCookie(cookie)
				request.Header.Set("X-CSRF-Token", credentials.CSRF)
				request.Header.Set("Content-Type", contentType)
				client := host.Client()
				client.Timeout = 5 * time.Second
				response, err = client.Do(request)
				if err != nil {
					_, pendingErr := os.Stat(databasePath + ".restore")
					t.Fatalf("maintenance response lost (restore already staged=%t): %v", pendingErr == nil, err)
				}
			}
			defer response.Body.Close()
			if response.StatusCode != want {
				t.Fatalf("status=%d want=%d", response.StatusCode, want)
			}
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "backup_response" {
				var receipt struct {
					Staged  bool `json:"staged"`
					Restart bool `json:"restart_required"`
				}
				if err := json.Unmarshal(raw, &receipt); err != nil || !receipt.Staged || !receipt.Restart {
					t.Fatal("missing durable staging acknowledgement")
				}
				if _, err := os.Stat(databasePath + ".restore"); err != nil {
					t.Fatal("pending restore missing", err)
				}
			} else if len(raw) == 0 {
				t.Fatal("empty backup")
			}
		})
	}
}
