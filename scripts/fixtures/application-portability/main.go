// Disposable whole-application portability driver. No production configuration
// or database is read, and no source directory or previous result is deleted.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentclient"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	_ "modernc.org/sqlite"
)

var head = os.Getenv("MYPROBE_TEST_SOURCE_COMMIT")

const applicationKey = "synthetic-capacity-application-key-never-production"
const passphrase = "synthetic-large-backup-passphrase"
const importedUser = "capacity-admin"
const importedPassword = "synthetic-capacity-admin-passphrase"

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func require(ok bool, why string) {
	if !ok {
		panic(why)
	}
}
func digest(file string) string {
	f, e := os.Open(file)
	must(e)
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	must(e)
	return hex.EncodeToString(h.Sum(nil))
}
func save(file string, v any) {
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(e)
	must(json.NewEncoder(f).Encode(v))
	must(f.Sync())
	must(f.Close())
}
func load(file string, v any) { b, e := os.ReadFile(file); must(e); must(json.Unmarshal(b, v)) }
func readonlyURI(file string) string {
	return "file:" + url.PathEscape(filepath.ToSlash(file)) + "?mode=ro&_pragma=query_only(1)"
}

type control struct {
	NodeID, AgentToken         string
	Rows, SourceBytes          int64
	MetricDigest, LedgerDigest string
}
type api struct {
	base, csrf string
	client     *http.Client
}

func newAPI(base string) *api {
	jar, e := cookiejar.New(nil)
	must(e)
	return &api{base: base, client: &http.Client{Jar: jar, Timeout: 300 * time.Second}}
}
func (a *api) request(method, path string, payload any, want int, out any) {
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		must(e)
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, a.base+path, body)
	must(e)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", a.csrf)
	r, e := a.client.Do(req)
	must(e)
	defer r.Body.Close()
	require(r.StatusCode == want, fmt.Sprintf("%s %s status%d expected%d", method, path, r.StatusCode, want))
	if out != nil {
		must(json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(out))
	} else {
		_, _ = io.Copy(io.Discard, r.Body)
	}
}
func (a *api) login(user, password string) {
	var out struct {
		CSRF string `json:"csrf_token"`
	}
	a.request("POST", "/api/v1/auth/login", map[string]string{"username": user, "password": password}, 200, &out)
	require(out.CSRF != "", "login missing CSRF")
	a.csrf = out.CSRF
}
func (a *api) stage(file string) {
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)
	must(writer.WriteField("passphrase", passphrase))
	_, e := writer.CreateFormFile("file", "archive.mpb")
	must(e)
	boundary := writer.Boundary()
	suffix := []byte("\r\n--" + boundary + "--\r\n")
	f, e := os.Open(file)
	must(e)
	defer f.Close()
	info, e := f.Stat()
	must(e)
	req, e := http.NewRequest("POST", a.base+"/api/v1/admin/maintenance/restore", io.MultiReader(bytes.NewReader(prefix.Bytes()), f, bytes.NewReader(suffix)))
	must(e)
	req.ContentLength = int64(prefix.Len()) + info.Size() + int64(len(suffix))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-CSRF-Token", a.csrf)
	r, e := a.client.Do(req)
	must(e)
	defer r.Body.Close()
	require(r.StatusCode == 202, fmt.Sprintf("streaming HTTP stage status%d", r.StatusCode))
	var out struct {
		Staged  bool `json:"staged"`
		Restart bool `json:"restart_required"`
	}
	must(json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&out))
	require(out.Staged && out.Restart, "pending restore not durably staged")
}
func (a *api) export(file string) {
	b, e := json.Marshal(map[string]string{"passphrase": passphrase})
	must(e)
	req, e := http.NewRequest("POST", a.base+"/api/v1/admin/maintenance/backup", bytes.NewReader(b))
	must(e)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", a.csrf)
	r, e := a.client.Do(req)
	must(e)
	defer r.Body.Close()
	require(r.StatusCode == 200, fmt.Sprintf("streaming HTTP export status%d", r.StatusCode))
	f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(e)
	_, e = io.Copy(f, r.Body)
	must(e)
	must(f.Sync())
	must(f.Close())
	info, e := os.Stat(file)
	must(e)
	require(info.Size() >= 512<<20, "HTTP archive below512MiB")
}

type server struct {
	cmd       *exec.Cmd
	done      chan error
	log       *os.File
	base      string
	readiness float64
	exit      string
}

func start(binary, database, root, label, initialPassword string) *server {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	must(e)
	address := l.Addr().String()
	must(l.Close())
	base := "http://" + address
	cmd := exec.Command(binary)
	cmd.Dir = root
	// Whitelist the process runtime essentials; never inherit operator MYPROBE_*.
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "HOME", "USERPROFILE", "LANG", "TMPDIR"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "MYPROBE_LISTEN="+address, "MYPROBE_DATABASE="+database, "MYPROBE_ADMIN_USERNAME=destination-operator", "MYPROBE_ADMIN_PASSWORD="+initialPassword, "MYPROBE_ENCRYPTION_KEY="+applicationKey, "MYPROBE_HTTP_PROBES_ENABLED=false", "MYPROBE_RELEASE_CHECKS_AUTO=false", "MYPROBE_NOTIFICATION_ORIGIN="+base, "MYPROBE_RAW_RETENTION_DAYS=3650", "MYPROBE_MINUTE_RETENTION_DAYS=3650", "MYPROBE_FIVE_MINUTE_RETENTION_DAYS=3650")
	log, e := os.OpenFile(filepath.Join(root, label+".private.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	must(e)
	cmd.Stdout = log
	cmd.Stderr = log
	began := time.Now()
	must(cmd.Start())
	s := &server{cmd: cmd, done: make(chan error, 1), log: log, base: base}
	go func() { s.done <- cmd.Wait() }()
	client := &http.Client{Timeout: time.Second}
	for time.Since(began) < 30*time.Second {
		select {
		case err := <-s.done:
			must(fmt.Errorf("Server exited before readiness: %w", err))
		default:
		}
		r, e := client.Get(base + "/healthz")
		if e == nil {
			ok := r.StatusCode == 200
			r.Body.Close()
			if ok {
				s.readiness = time.Since(began).Seconds()
				return s
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.stop()
	panic("unchanged30-second actual Server readiness exceeded")
}
func (s *server) stop() {
	if s == nil || s.cmd == nil {
		return
	}
	if runtime.GOOS == "windows" {
		_ = s.cmd.Process.Kill()
		s.exit = "Windows test-owned process termination; no graceful-stop claim"
	} else {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
		s.exit = "SIGTERM"
	}
	select {
	case err := <-s.done:
		if runtime.GOOS != "windows" {
			must(err)
		}
	case <-time.After(20 * time.Second):
		_ = s.cmd.Process.Kill()
		<-s.done
		panic("owned Server stop timeout")
	}
	must(s.log.Close())
	s.cmd = nil
}

func rowsDigest(db *sql.DB, query string) (string, int64) {
	rows, e := db.Query(query)
	must(e)
	defer rows.Close()
	h := sha256.New()
	var count int64
	for rows.Next() {
		var a, b string
		must(rows.Scan(&a, &b))
		fmt.Fprintf(h, "%d:", len(a))
		_, e = io.WriteString(h, a)
		must(e)
		fmt.Fprintf(h, "%d:", len(b))
		_, e = io.WriteString(h, b)
		must(e)
		count++
	}
	must(rows.Err())
	return hex.EncodeToString(h.Sum(nil)), count
}
func inspect(database string, c control, extra int64) map[string]any {
	db, e := sql.Open("sqlite", readonlyURI(database))
	must(e)
	defer db.Close()
	db.SetMaxOpenConns(1)
	var nodes, users, channels, reports int64
	var schema, engine string
	must(db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&nodes))
	must(db.QueryRow("SELECT COUNT(*) FROM users").Scan(&users))
	must(db.QueryRow("SELECT COUNT(*) FROM notification_channels").Scan(&channels))
	must(db.QueryRow("SELECT COUNT(*) FROM metric_samples").Scan(&reports))
	must(db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&schema))
	must(db.QueryRow("SELECT sqlite_version()").Scan(&engine))
	require(nodes == 1 && users == 1 && channels == 1 && reports == c.Rows+extra, "recovered application population differs")
	metric, count := rowsDigest(db, fmt.Sprintf("SELECT captured_at,report_json FROM metric_samples ORDER BY id LIMIT %d", c.Rows))
	require(metric == c.MetricDigest && count == c.Rows, "complete ordered report bytes differ")
	ledger, count := rowsDigest(db, "SELECT request_id,request_digest||result_json FROM service_alert_imports ORDER BY request_id")
	require(ledger == c.LedgerDigest && count == 1, "permanent receipt digest differs")
	return map[string]any{"nodes": nodes, "users": users, "channels": channels, "reports": reports, "original_reports_checked": c.Rows, "metric_digest": metric, "ledger_digest": ledger, "schema": schema, "sqlite_version": engine}
}

func main() {
	require(regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(head), "MYPROBE_TEST_SOURCE_COMMIT must identify the exact source; bind the executable independently")
	require(len(os.Args) == 7, "usage: driver ROOT SERVER INPUT_BUNDLE EXPECTED_ORIGIN EXPECTED_SOURCE export|verify")
	root, binary, bundle, origin, expected, mode := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6]
	require(mode == "export" || mode == "verify", "invalid mode")
	for _, p := range []string{root, binary, bundle} {
		require(filepath.IsAbs(p), "absolute paths required")
		info, e := os.Lstat(p)
		must(e)
		require(info.Mode()&os.ModeSymlink == 0, "symlink path rejected")
	}
	entries, e := os.ReadDir(root)
	must(e)
	require(len(entries) == 0, "fresh empty owned root required")
	var c control
	load(filepath.Join(bundle, "control.json"), &c)
	require(c.Rows > 0 && c.SourceBytes >= 512<<20 && c.AgentToken != "", "meaningful private capacity input required")
	var producer struct {
		OS, Arch, SourceCommit, ArchiveSHA256 string
		ArchiveBytes                          int64
	}
	load(filepath.Join(bundle, "producer.json"), &producer)
	require(producer.OS+"/"+producer.Arch == origin && producer.SourceCommit == expected, "input platform/source differs")
	input := filepath.Join(bundle, "archive.mpb")
	inputSHA := digest(input)
	info, e := os.Stat(input)
	must(e)
	require(inputSHA == producer.ArchiveSHA256 && info.Size() == producer.ArchiveBytes, "complete input archive transfer differs")
	password := rand.Text() + rand.Text()
	database := filepath.Join(root, "server.db")
	began := time.Now()
	s := start(binary, database, root, "before-stage", password)
	defer func() { s.stop() }()
	firstReady := s.readiness
	a := newAPI(s.base)
	a.login("destination-operator", password)
	var marker struct {
		Node struct {
			ID string `json:"id"`
		} `json:"node"`
	}
	a.request("POST", "/api/v1/admin/nodes", map[string]string{"name": "Synthetic pre-restore destination marker"}, 201, &marker)
	require(marker.Node.ID != "", "destination marker absent")
	a.stage(input)
	_, e = os.Stat(database + ".restore")
	must(e)
	s.stop()
	s = start(binary, database, root, "after-activation", password)
	secondReady := s.readiness
	// An authenticated old destination session must not survive activation.
	a.base = s.base
	a.request("GET", "/api/v1/auth/me", nil, 401, nil)
	a = newAPI(s.base)
	a.login(importedUser, importedPassword)
	old := newAPI(s.base)
	old.request("POST", "/api/v1/auth/login", map[string]string{"username": "destination-operator", "password": password}, 401, nil)
	state := inspect(database, c, 0)
	_, e = os.Stat(database + ".restore")
	require(os.IsNotExist(e), "pending restore still present after activation")
	prior, e := filepath.Glob(database + ".pre-restore-*")
	must(e)
	preserved := false
	for _, file := range prior {
		if strings.HasSuffix(file, "-wal") || strings.HasSuffix(file, "-shm") {
			continue
		}
		db, e := sql.Open("sqlite", readonlyURI(file))
		must(e)
		var count int
		must(db.QueryRow("SELECT COUNT(*) FROM nodes WHERE id=?", marker.Node.ID).Scan(&count))
		must(db.Close())
		if count == 1 {
			preserved = true
		}
	}
	require(preserved, "original destination database marker not preserved")
	if mode == "export" {
		a.export(filepath.Join(root, "archive.mpb"))
		f, e := os.OpenFile(filepath.Join(root, "control.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		must(e)
		source, e := os.Open(filepath.Join(bundle, "control.json"))
		must(e)
		_, e = io.Copy(f, source)
		must(e)
		must(source.Close())
		must(f.Close())
		info, e := os.Stat(filepath.Join(root, "archive.mpb"))
		must(e)
		save(filepath.Join(root, "producer.json"), map[string]any{"Format": "myprobe.application-portability-producer.v1", "SourceCommit": head, "OS": runtime.GOOS, "Arch": runtime.GOARCH, "ArchiveSHA256": digest(filepath.Join(root, "archive.mpb")), "ArchiveBytes": info.Size(), "Reports": c.Rows, "SourceBytes": c.SourceBytes, "ProducedBy": "actual cmd/server streaming HTTP export after actual application restore/restart"})
	}
	// Exercise the actual Client.Run and production Gateway using the original
	// Agent credential carried in the transferred private fixture control.
	ctx, cancel := context.WithCancel(context.Background())
	client, e := agentclient.New(agentclient.Config{ServerURL: s.base, Token: c.AgentToken, AgentVersion: "private-app-portability", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	must(e)
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	stored := int64(0)
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		db, e := sql.Open("sqlite", readonlyURI(database))
		must(e)
		var n int64
		must(db.QueryRow("SELECT COUNT(*) FROM metric_samples WHERE node_id=?", c.NodeID).Scan(&n))
		must(db.Close())
		if n > c.Rows {
			stored = n - c.Rows
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		panic("actual Client.Run did not stop")
	}
	require(stored > 0, "original Agent credential did not store a new report through actual Gateway")
	s.stop()
	db, e := sql.Open("sqlite", readonlyURI(database))
	must(e)
	var finalReports int64
	must(db.QueryRow("SELECT COUNT(*) FROM metric_samples WHERE node_id=?", c.NodeID).Scan(&finalReports))
	must(db.Close())
	require(finalReports-c.Rows >= stored, "new original-credential reports disappeared")
	stored = finalReports - c.Rows
	final := inspect(database, c, stored)
	require(digest(input) == inputSHA, "input encrypted archive changed")
	result := map[string]any{"format": "myprobe.application-portability-result.v1", "source_commit": head, "os": runtime.GOOS, "arch": runtime.GOARCH, "source_origin": origin, "input_archive_source_commit": producer.SourceCommit, "input_archive_sha256": inputSHA, "input_archive_bytes": producer.ArchiveBytes, "server_sha256": digest(binary), "readiness_seconds": []float64{firstReady, secondReady}, "before_agent": state, "after_agent": final, "new_original_credential_reports": stored, "destination_database_preserved": preserved, "old_destination_session_rejected": true, "old_destination_password_rejected": true, "stored_source_admin_login": true, "pending_restore_activated": true, "input_archive_unchanged": true, "elapsed_seconds": time.Since(began).Seconds(), "passed": true, "scope": "Meaningful512MiB synthetic data, actual production HTTP streaming stage and export, pending activation at real Server restart, exact original report bytes/permanent receipt, stored source login, destination credential isolation/preserved old database and actual Client.Run/Gateway report with original Agent token. Separate extended3650-day retention preserves the exact snapshot; default-retention capacity/24h/old-binary rollback and live failover are separate gates."}
	save(filepath.Join(root, "application-result.json"), result)
	fmt.Printf("whole application %s/%s: HTTP stage/restart, %d exact source reports and %d new original-credential Gateway reports passed\n", runtime.GOOS, runtime.GOARCH, c.Rows, stored)
}
