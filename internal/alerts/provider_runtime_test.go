package alerts

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func providerRuntime(t *testing.T, sender *HTTPSender, kind string, config ChannelConfig, count int) (*store.Store, *Service, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "provider.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := New(db, "synthetic-provider-key-0123456789", sender, nil, "https://"+strings.Repeat("a", 504))
	channel, err := svc.CreateChannel(ctx, "provider fixture", kind, config)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	for n := 0; n < count; n++ {
		node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: strings.Repeat("<>& *_[]\\ 😀", 8)})
		if err != nil {
			t.Fatal(err)
		}
		expiry := at.Add(time.Hour)
		if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, ExpiresAt: &expiry, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "provider", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "provider", Kind: "expiry", ChannelID: channel.ID, Config: json.RawMessage(`{"days_before":1,"aggregation_seconds":5}`), CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, at.Add(-6*time.Second)); err != nil {
		t.Fatal(err)
	}
	return db, svc, at
}
func providerContext(t *testing.T, db *store.Store) store.AggregateContext {
	t.Helper()
	page, err := db.ListNotificationAggregates(context.Background(), 0, 25)
	if err != nil || len(page.Aggregates) != 1 {
		t.Fatalf("group index: %+v %v", page, err)
	}
	detail, err := db.AggregateContext(context.Background(), page.Aggregates[0].ID, 0, 25)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestAggregateHTTPProviderRuntime(t *testing.T) {
	for _, kind := range []string{"webhook", "telegram", "telegram-HTML", "telegram-MarkdownV2", "discord"} {
		t.Run(kind, func(t *testing.T) {
			var raw []byte
			var key string
			var mu sync.Mutex
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var err error
				raw, err = io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				key = r.Header.Get("Idempotency-Key")
				if strings.HasPrefix(kind, "telegram") {
					_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":-100}}}`)
				} else if kind == "discord" {
					if r.URL.Query().Get("wait") != "true" {
						t.Error("no persistence confirmation")
					}
					_, _ = io.WriteString(w, `{"id":"123","channel_id":"456"}`)
				} else {
					w.WriteHeader(204)
				}
			}))
			defer receiver.Close()
			sender := NewHTTPSender(receiver.Client())
			sender.telegramBaseURL = receiver.URL
			config := ChannelConfig{URL: receiver.URL, BotToken: "fixture:token", ChatID: "-100"}
			provider := kind
			if strings.HasPrefix(kind, "telegram") {
				provider = "telegram"
				config.ParseMode = strings.TrimPrefix(strings.TrimPrefix(kind, "telegram"), "-")
			}
			db, svc, at := providerRuntime(t, sender, provider, config, 10)
			if worked, err := svc.DeliverOne(context.Background(), at); err != nil || !worked {
				t.Fatalf("deliver: %v %v", worked, err)
			}
			mu.Lock()
			defer mu.Unlock()
			detail := providerContext(t, db)
			if detail.Totals.Delivered != 10 || detail.Aggregate.Status != "finished" || len(detail.Attempts) != 1 || len(detail.Attempts[0].Content.Members) != 10 || len(raw) == 0 {
				t.Fatalf("provider result: %+v", detail)
			}
			for _, ref := range detail.Attempts[0].Members {
				if !ref.Included {
					t.Fatal("authorized member missing")
				}
			}
			if provider == "webhook" {
				var n Notification
				if err := json.Unmarshal(raw, &n); err != nil || len(raw) > store.AggregatePayloadByteLimit || len(n.Members) != 10 || n.GroupPath != detail.Attempts[0].Content.GroupPath || key == "" {
					t.Fatal("webhook escaped-data bound or references")
				}
			}
			if provider == "telegram" {
				var payload struct {
					Text string `json:"text"`
				}
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatal(err)
				}
				logical := Notification{AggregateID: detail.Aggregate.ID, Title: detail.Attempts[0].Content.Title, Message: detail.Attempts[0].Content.Message, GroupPath: detail.Attempts[0].Content.GroupPath}
				if payload.Text != telegramContent(logical, config.ParseMode) {
					t.Fatal("provider did not render its frozen logical content")
				}
			}
			if provider == "discord" {
				var payload struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal(raw, &payload); err != nil || textUnits(payload.Content) > 2000 || !strings.Contains(payload.Content, detail.Attempts[0].Content.GroupPath) {
					t.Fatal("Discord bound or content link")
				}
			}
		})
	}
}

func TestAggregateProviderRateAndUnknownRetry(t *testing.T) {
	for _, kind := range []string{"telegram", "discord"} {
		t.Run(kind, func(t *testing.T) {
			bodies := [][]byte{}
			var mu sync.Mutex
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				bodies = append(bodies, raw)
				if len(bodies) == 1 {
					if kind == "telegram" {
						w.WriteHeader(429)
						_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"parameters":{"retry_after":123},"description":"private-provider-marker"}`)
					} else {
						_, _ = io.WriteString(w, `{"id":"123"}`)
					}
					return
				}
				if kind == "telegram" {
					_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":-100}}}`)
				} else {
					_, _ = io.WriteString(w, `{"id":"123","channel_id":"456"}`)
				}
			}))
			defer receiver.Close()
			sender := NewHTTPSender(receiver.Client())
			sender.telegramBaseURL = receiver.URL
			db, svc, at := providerRuntime(t, sender, kind, ChannelConfig{URL: receiver.URL, BotToken: "fixture:token", ChatID: "-100"}, 2)
			if _, err := svc.DeliverOne(context.Background(), at); err != nil {
				t.Fatal(err)
			}
			first := providerContext(t, db)
			want := "failed"
			if kind == "discord" {
				want = "unknown"
			}
			if first.Totals.Pending != 2 || first.Attempts[0].Outcome != want {
				t.Fatalf("response classification: %+v", first)
			}
			due := first.Members[0].AvailableAt
			if kind == "telegram" && due.Before(at.Add(123*time.Second)) {
				t.Fatal("body retry_after ignored")
			}
			if worked, err := svc.DeliverOne(context.Background(), due.Add(-time.Nanosecond)); err != nil || worked {
				t.Fatal("provider retry ran early")
			}
			if worked, err := svc.DeliverOne(context.Background(), due); err != nil || !worked {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			last := providerContext(t, db)
			if last.Totals.Delivered != 2 || len(last.Attempts) != 2 || last.Attempts[1].Outcome != want || string(bodies[0]) != string(bodies[1]) || last.Attempts[0].Content.SnapshotID != last.Attempts[1].Content.SnapshotID {
				t.Fatal("retry changed content or lost history")
			}
			raw, _ := json.Marshal(last)
			if strings.Contains(string(raw), "fixture:token") || strings.Contains(string(raw), "private-provider-marker") {
				t.Fatal("private provider details persisted")
			}
		})
	}
}

func smtpTLSFixture(t *testing.T, dropFirst bool, mode string) (ChannelConfig, *x509.CertPool, *[][]byte, *sync.Mutex) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	bodies := [][]byte{}
	mu := &sync.Mutex{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				tlsActive := mode == "tls"
				if tlsActive {
					conn = tls.Server(conn, serverTLS)
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := textproto.NewReader(bufio.NewReader(conn))
				write := func(text string) bool {
					_, err := fmt.Fprint(conn, text)
					if err != nil {
						t.Error(err)
					}
					return err == nil
				}
				if !write("220 fixture ESMTP\r\n") {
					return
				}
				for {
					line, err := reader.ReadLine()
					if err != nil {
						t.Error(err)
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						features := "250-fixture\r\n"
						if !tlsActive {
							features += "250-STARTTLS\r\n"
						}
						features += "250 SIZE 1048576\r\n"
						if !write(features) {
							return
						}
					case line == "STARTTLS":
						if tlsActive || !write("220 start TLS\r\n") {
							return
						}
						secure := tls.Server(conn, serverTLS)
						if err := secure.Handshake(); err != nil {
							t.Error(err)
							return
						}
						conn = secure
						tlsActive = true
						reader = textproto.NewReader(bufio.NewReader(conn))
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						if !tlsActive {
							t.Error("mail began before TLS")
							return
						}
						if !write("250 OK\r\n") {
							return
						}
					case line == "DATA":
						if !write("354 send content\r\n") {
							return
						}
						body, err := reader.ReadDotBytes()
						if err != nil {
							t.Error(err)
							return
						}
						mu.Lock()
						bodies = append(bodies, body)
						count := len(bodies)
						mu.Unlock()
						if dropFirst && count == 1 {
							return
						}
						if !write("250 accepted\r\n") {
							return
						}
					case line == "QUIT":
						write("221 bye\r\n")
						return
					default:
						t.Errorf("unexpected SMTP command %q", line)
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); <-done })
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	number, _ := strconv.Atoi(port)
	return ChannelConfig{SMTPHost: "127.0.0.1", SMTPPort: number, SMTPFrom: "sender@example.com", SMTPTo: "recipient@example.com", SMTPEncryption: mode}, roots, &bodies, mu
}

func TestAggregateSMTPRuntimeAcceptanceAndLostAck(t *testing.T) {
	for _, mode := range []string{"tls", "starttls"} {
		for _, drop := range []bool{false, true} {
			t.Run(fmt.Sprint(mode+"/drop-first=", drop), func(t *testing.T) {
				config, roots, bodies, mu := smtpTLSFixture(t, drop, mode)
				sender := NewHTTPSender(&http.Client{})
				sender.smtpRoots = roots
				db, svc, at := providerRuntime(t, sender, "smtp", config, 10)
				if _, err := svc.DeliverOne(context.Background(), at); err != nil {
					t.Fatal(err)
				}
				first := providerContext(t, db)
				if drop {
					if first.Totals.Pending != 10 || first.Attempts[0].Outcome != "unknown" || first.Attempts[0].ErrorClass != "smtp_delivery" {
						t.Fatal("lost DATA ack not unknown")
					}
					if _, err := svc.DeliverOne(context.Background(), first.Members[0].AvailableAt); err != nil {
						t.Fatal(err)
					}
				}
				last := providerContext(t, db)
				if last.Totals.Delivered != 10 {
					t.Fatal("SMTP group not accepted")
				}
				mu.Lock()
				defer mu.Unlock()
				if len(*bodies) != 1+map[bool]int{false: 0, true: 1}[drop] {
					t.Fatal("SMTP request count")
				}
				if drop && string((*bodies)[0]) != string((*bodies)[1]) {
					t.Fatal("MIME retry changed")
				}
				message, err := mail.ReadMessage(strings.NewReader(string((*bodies)[0])))
				if err != nil {
					t.Fatal(err)
				}
				title, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
				if err != nil || title != last.Attempts[0].Content.Title {
					t.Fatal("MIME subject mismatch")
				}
				body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
				if err != nil || strings.TrimSuffix(string(body), "\n") != last.Attempts[0].Content.Message {
					t.Fatal("MIME body or content link mismatch")
				}
				for _, b := range (*bodies)[0] {
					if b > 127 {
						t.Fatal("requires unadvertised SMTP 8BITMIME")
					}
				}
			})
		}
	}
}

func TestAggregateWebhookTemplateTimeSurvivesTimezoneAndRestore(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("fixture-east", 8*3600)
	defer func() { time.Local = previous }()
	var mu sync.Mutex
	var bodies, keys []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		count := len(bodies)
		mu.Unlock()
		if r.Header.Get("Content-Type") != "text/plain" {
			t.Error("custom content type lost")
		}
		if count == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	sender := NewHTTPSender(receiver.Client())
	db, svc, at := providerRuntime(t, sender, "webhook", ChannelConfig{URL: receiver.URL, BodyTemplate: "{{event.time}}|{{title}}", Headers: map[string]string{"Content-Type": "text/plain"}}, 2)
	if _, err := svc.DeliverOne(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	first := providerContext(t, db)
	backup := filepath.Join(t.TempDir(), "timezone-backup.db")
	ctx := context.Background()
	if err := db.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	path := db.DatabasePath()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	time.Local = time.UTC
	if _, err := store.StageDatabaseRestore(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyPendingRestore(ctx, path, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	svc = New(restored, "synthetic-provider-key-0123456789", sender, nil)
	if _, err := svc.DeliverOne(ctx, first.Members[0].AvailableAt); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || bodies[0] != bodies[1] || keys[0] != keys[1] {
		t.Fatalf("restored template time changed: %q", bodies)
	}
}
