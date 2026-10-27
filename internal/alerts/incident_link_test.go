package alerts

import (
	"context"
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestIncidentLinkProviderEnvelopes(t *testing.T) {
	path := "https://probe.example.test/admin?incident=original"
	for _, mode := range []string{"webhook", "custom", "telegram", "HTML", "MarkdownV2", "discord"} {
		t.Run(mode, func(t *testing.T) {
			var body string
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				w.Header().Set("Content-Type", "application/json")
				if mode == "discord" {
					io.WriteString(w, `{"id":"1","channel_id":"2"}`)
				} else if mode == "webhook" || mode == "custom" {
					w.WriteHeader(204)
				} else {
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
				config.BodyTemplate = `{"link":"{{incident.url}}","text":"{{message}}"}`
			case "HTML", "MarkdownV2":
				kind = "telegram"
				config.ParseMode = mode
			}
			n := Notification{Title: "title", Message: "body", IncidentID: "original", IncidentPath: path, Timestamp: time.Now()}
			if err := sender.Deliver(context.Background(), kind, config, n); err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal([]byte(body), &fields); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "webhook":
				if fields["incident_path"] != path {
					t.Fatal(body)
				}
			case "custom":
				if fields["link"] != path {
					t.Fatal(body)
				}
			case "discord":
				if !strings.Contains(fields["content"].(string), "[查看事件](<"+path+">)") {
					t.Fatal(body)
				}
			default:
				if !strings.Contains(fields["text"].(string), path) {
					t.Fatal(body)
				}
			}
		})
	}
	raw, err := smtpMessage(ChannelConfig{SMTPFrom: "from@example.test", SMTPTo: "to@example.test"}, Notification{Title: "故障", Message: "正文", IncidentPath: path})
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil || string(decoded) != "正文\n查看事件："+path { // MIME normalizes line endings.
		if err != nil || strings.ReplaceAll(string(decoded), "\r\n", "\n") != "正文\n查看事件："+path {
			t.Fatalf("SMTP link: %q %v", decoded, err)
		}
	}
}

func TestIncidentLinkRetainsTemplateContractAndRejectsUnsafeURLs(t *testing.T) {
	old := Notification{Title: "<b>title</b>", Message: "body {{incident.url}}"}
	if got := telegramContent(old, "HTML"); got != old.Title+"\n"+old.Message {
		t.Fatal("legacy markup changed")
	}
	old.IncidentPath = "https://example.test/admin?incident=one"
	if got := renderTemplate("{{message}} | {{incident.url}}", old); got != "body {{incident.url}} | "+old.IncidentPath {
		t.Fatal("recursive template substitution")
	}
	for _, path := range []string{"javascript:alert(1)", "//outside.test/admin", "https://user:secret@example.test/admin", "https://example.test/\nprivate"} {
		if got := incidentContentSuffix(path, "HTML"); got != "" {
			t.Fatal("unsafe URL was emitted")
		}
	}
	if got := incidentContentSuffix("/admin?incident=one", "MarkdownV2"); !strings.Contains(got, `incident\=one`) {
		t.Fatal("relative URL is not literal")
	}
}

func TestIncidentLinkProviderLengthBudgetIncludesSuffix(t *testing.T) {
	for _, kind := range []string{"telegram", "discord"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) }))
			defer receiver.Close()
			sender := NewHTTPSender(receiver.Client())
			sender.telegramBaseURL = receiver.URL
			limit := 4096
			if kind == "discord" {
				limit = 2000
			}
			n := Notification{Message: strings.Repeat("😀", limit/2), IncidentPath: "https://" + strings.Repeat("a", 504) + "/admin?incident=original"}
			err := sender.Deliver(context.Background(), kind, ChannelConfig{URL: receiver.URL, BotToken: "fixture", ChatID: "2"}, n)
			if err == nil || calls != 0 {
				t.Fatal("oversized linked message reached provider")
			}
		})
	}
}
