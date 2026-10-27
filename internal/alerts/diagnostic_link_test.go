package alerts

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/notificationlink"
)

func TestServiceDiagnosticProviderRenderingAndBudgets(t *testing.T) {
	link := notificationlink.ServiceDiagnosticAt("https://notify.example.test", "service", "observer", time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	n := Notification{ServiceID: "service", ServiceDiagnosticPath: link, IncidentPath: notificationlink.Incident("https://notify.example.test", "incident"), Title: "safe <alias>", Message: "<b>safe body</b>"}
	for _, mode := range []string{"webhook", "custom", "discord", "HTML", "MarkdownV2", "telegram"} {
		t.Run(mode, func(t *testing.T) {
			var payload string
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				payload = string(body)
				switch mode {
				case "discord":
					io.WriteString(w, `{"id":"1","channel_id":"2"}`)
				case "webhook", "custom":
					w.WriteHeader(204)
				default:
					io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":2}}}`)
				}
			}))
			defer receiver.Close()
			sender := NewHTTPSender(receiver.Client())
			sender.telegramBaseURL = receiver.URL
			config := ChannelConfig{URL: receiver.URL, BotToken: "fixture", ChatID: "2"}
			kind := mode
			switch mode {
			case "custom":
				kind = "webhook"
				config.BodyTemplate = `{"diagnostic":"{{service.diagnostic.url}}","incident":"{{incident.url}}"}`
			case "HTML", "MarkdownV2":
				kind = "telegram"
				config.ParseMode = mode
			}
			if err := sender.Deliver(context.Background(), kind, config, n); err != nil {
				t.Fatal(err)
			}
			var data map[string]any
			if json.Unmarshal([]byte(payload), &data) != nil {
				t.Fatal("invalid provider JSON")
			}
			switch mode {
			case "webhook":
				if data["service_diagnostic_path"] != link {
					t.Fatal("webhook field")
				}
			case "custom":
				if data["diagnostic"] != link || data["incident"] != n.IncidentPath {
					t.Fatal("custom field")
				}
			case "discord":
				if !strings.Contains(data["content"].(string), "[查看服务诊断](<"+link+">)") {
					t.Fatal("discord link")
				}
			case "HTML":
				if !strings.Contains(data["text"].(string), html.EscapeString(link)) || strings.Contains(data["text"].(string), "<b>safe body</b>") {
					t.Fatal("HTML escaping")
				}
			default:
				if !strings.Contains(data["text"].(string), link) {
					t.Fatal("telegram link")
				}
			}
		})
	}
	raw, err := smtpMessage(ChannelConfig{SMTPFrom: "from@example.test", SMTPTo: "to@example.test"}, n)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(m.Body))
	if err != nil || !strings.Contains(strings.ReplaceAll(string(body), "\r\n", "\n"), "查看服务诊断："+link) {
		t.Fatal("SMTP diagnostic link")
	}
	for _, unsafe := range []string{"javascript:alert(1)", "//outside.test/admin?service=a", "https://user:secret@example.test/admin", link + "&observer=other", strings.Replace(link, "service=service", "target=private", 1), link + "#fragment"} {
		if diagnosticContentSuffix(unsafe, "HTML") != "" {
			t.Fatal("unsafe diagnostic link")
		}
	}
	// Links count toward provider limits; oversize is rejected before network IO.
	hits := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(204) }))
	defer receiver.Close()
	n.Message = strings.Repeat("x", 1990)
	if err := NewHTTPSender(receiver.Client()).Deliver(context.Background(), "discord", ChannelConfig{URL: receiver.URL}, n); err == nil || hits != 0 {
		t.Fatal("oversized linked content sent")
	}
}
