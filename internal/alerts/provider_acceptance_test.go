package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"math"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestProviderRequiresValidConfirmation(t *testing.T) {
	cases := []struct {
		name, provider, body string
		status               int
		class                string
		permanent, ambiguous bool
		delay                time.Duration
	}{
		{"tg-ok", "telegram", `{"ok":true,"result":{"message_id":1,"chat":{"id":-100}}}`, 200, "", false, false, 0},
		{"tg-reject", "telegram", `{"ok":false,"error_code":400,"description":"private-provider-marker"}`, 200, "http_client_error", true, false, 0},
		{"tg-rate", "telegram", `{"ok":false,"error_code":429,"parameters":{"retry_after":123}}`, 200, "http_rate_limited", false, false, 123 * time.Second},
		{"tg-http-rate", "telegram", `{"ok":false,"error_code":429,"parameters":{"retry_after":123}}`, 429, "http_rate_limited", false, false, 123 * time.Second},
		{"tg-result-missing", "telegram", `{"ok":true}`, 200, "telegram_invalid_response", false, true, 0},
		{"tg-fake-message", "telegram", `{"ok":true,"result":{"message_id":0,"chat":{"id":-100}}}`, 200, "telegram_invalid_response", false, true, 0},
		{"tg-json-invalid", "telegram", `{"ok":true,`, 200, "telegram_invalid_response", false, true, 0},
		{"tg-trailing", "telegram", `{"ok":true,"result":{"message_id":1,"chat":{"id":-100}}}{}`, 200, "telegram_invalid_response", false, true, 0},
		{"discord-ok", "discord", `{"id":"123","channel_id":"456"}`, 200, "", false, false, 0},
		{"discord-empty", "discord", "", 204, "discord_invalid_response", false, true, 0},
		{"discord-nonnumeric", "discord", `{"id":"private-provider-marker","channel_id":"456"}`, 200, "discord_invalid_response", false, true, 0},
		{"discord-incomplete", "discord", `{"id":"123"}`, 200, "discord_invalid_response", false, true, 0},
		{"discord-oversized", "discord", `{"id":"123","channel_id":"456","pad":"` + strings.Repeat("x", providerResponseByteLimit) + `"}`, 200, "discord_invalid_response", false, true, 0},
		{"discord-reject", "discord", `{"message":"private-provider-marker"}`, 400, "http_client_error", true, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.provider == "discord" && (r.URL.Query().Get("wait") != "true" || r.URL.Query().Get("thread_id") != "42") {
					t.Error("confirmation query did not preserve thread")
				}
				w.WriteHeader(c.status)
				_, _ = io.WriteString(w, c.body)
			}))
			defer receiver.Close()
			sender := NewHTTPSender(receiver.Client())
			sender.telegramBaseURL = receiver.URL
			config := ChannelConfig{URL: receiver.URL + "?wait=false&thread_id=42", BotToken: "fixture:token", ChatID: "-100"}
			err := sender.Deliver(context.Background(), c.provider, config, Notification{Title: "fixture", Message: "body"})
			if c.class == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var result *DeliveryError
			if !errors.As(err, &result) || result.Class != c.class || result.Permanent != c.permanent || result.Ambiguous != c.ambiguous || result.RetryAfter != c.delay {
				t.Fatalf("result: %+v %v", result, err)
			}
			if strings.Contains(err.Error(), "private-provider-marker") {
				t.Fatal("provider data exposed")
			}
		})
	}
}

func TestProviderFractionalRetryDeadline(t *testing.T) {
	for raw, want := range map[string]time.Duration{"123.0000000001": 123*time.Second + 1, "1e-9": 1, "0.0000000001": 1, "-1": 0, "NaN": 0, "Inf": 0, "1e999": time.Duration(math.MaxInt64)} {
		if got := providerRetryDelay(raw); got != want {
			t.Fatalf("%s: %v want %v", raw, got, want)
		}
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "123.0000000001")
		w.WriteHeader(429)
		_, _ = io.WriteString(w, `{"retry_after":120.5,"message":"private-marker"}`)
	}))
	defer receiver.Close()
	err := NewHTTPSender(receiver.Client()).Deliver(context.Background(), "discord", ChannelConfig{URL: receiver.URL}, Notification{Title: "fixture"})
	var classified *DeliveryError
	if !errors.As(err, &classified) || classified.RetryAfter != 123*time.Second+1 || classified.Permanent || classified.Ambiguous {
		t.Fatalf("fractional rate limit: %+v %v", classified, err)
	}
}

func TestAggregateProviderEscapingAndContentLink(t *testing.T) {
	link := "https://probe.example.com/admin?aggregate=group&snapshot=" + strings.Repeat("a", 64)
	n := Notification{AggregateID: "group", Title: "MyProbe <汇总>", GroupPath: link, Message: "<b>& '_*[]()~`>#+-=|{}.!\\ @everyone 😀\n查看本次内容：" + link}
	for _, mode := range []string{"", "HTML", "MarkdownV2"} {
		t.Run("telegram-"+mode, func(t *testing.T) {
			var mu sync.Mutex
			var payload struct {
				Text      string `json:"text"`
				ParseMode string `json:"parse_mode"`
				ThreadID  int    `json:"message_thread_id"`
			}
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":-100}}}`)
			}))
			defer receiver.Close()
			sender := NewHTTPSender(receiver.Client())
			sender.telegramBaseURL = receiver.URL
			if err := sender.Deliver(context.Background(), "telegram", ChannelConfig{BotToken: "fixture:token", ChatID: "-100", ThreadID: "23", ParseMode: mode}, n); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if payload.ParseMode != mode || payload.ThreadID != 23 {
				t.Fatal("destination envelope changed")
			}
			switch mode {
			case "":
				if payload.Text != n.Title+"\n"+n.Message {
					t.Fatal("plain payload changed")
				}
			case "HTML":
				if strings.Contains(payload.Text, "<b>") || !strings.Contains(payload.Text, "&lt;b&gt;&amp;") || !strings.Contains(payload.Text, `<a href="`+html.EscapeString(link)+`">查看本次内容</a>`) {
					t.Fatal(payload.Text)
				}
			case "MarkdownV2":
				if !strings.Contains(payload.Text, `\_\*\[\]\(\)\~\`+"`"+`\>\#\+\-\=\|\{\}\.\!\\`) || !strings.HasSuffix(payload.Text, "[查看本次内容]("+link+")") {
					t.Fatal(payload.Text)
				}
			}
		})
	}
	t.Run("discord", func(t *testing.T) {
		var mu sync.Mutex
		var body struct {
			Content string `json:"content"`
			Allowed struct {
				Parse []string `json:"parse"`
			} `json:"allowed_mentions"`
		}
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `{"id":"123","channel_id":"456"}`)
		}))
		defer receiver.Close()
		if err := NewHTTPSender(receiver.Client()).Deliver(context.Background(), "discord", ChannelConfig{URL: receiver.URL}, n); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(body.Allowed.Parse) != 0 || !strings.Contains(body.Content, `\_\*\[\]`) || !strings.HasSuffix(body.Content, "[查看本次内容](<"+link+">)") {
			t.Fatal(body.Content)
		}
	})
	relative := n
	relative.GroupPath = "/admin?aggregate=group"
	relative.Message = "node\n查看本次内容：" + relative.GroupPath
	if strings.Contains(telegramContent(relative, "HTML"), "<a ") || strings.Contains(telegramContent(relative, "MarkdownV2"), "](") {
		t.Fatal("relative target emitted as invalid Telegram link")
	}
	single := n
	single.AggregateID = ""
	if telegramContent(single, "HTML") != single.Title+"\n"+single.Message {
		t.Fatal("single-template formatting changed")
	}
}

func TestProviderLocalBoundsPreserveAggregateLink(t *testing.T) {
	link := "https://" + strings.Repeat("a", 504) + "/admin?aggregate=" + strings.Repeat("b", 32) + "&snapshot=" + strings.Repeat("c", 64)
	n := Notification{AggregateID: "group", Title: "汇总", GroupPath: link, Message: strings.Repeat("*_[]\\", 145) + "\n查看本次内容：" + link}
	if len(n.Message) > store.AggregateMessageByteLimit {
		t.Fatal("fixture outside logical text contract")
	}
	text := discordContent(n)
	if textUnits(text) > 2000 || !strings.Contains(text, "…") || !strings.HasSuffix(text, "[查看本次内容](<"+link+">)") || strings.Contains(text, "\\…") {
		t.Fatal("escaped excerpt cut or link lost")
	}
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	sender := NewHTTPSender(receiver.Client())
	sender.telegramBaseURL = receiver.URL
	for _, provider := range []string{"discord", "telegram", "webhook"} {
		notification := Notification{Title: "x", Message: strings.Repeat("😀", 4096)}
		config := ChannelConfig{URL: receiver.URL, BotToken: "fixture:token", ChatID: "-100"}
		if provider == "webhook" {
			notification.AggregateID = "group"
			notification.Message = "small"
			config.BodyTemplate = strings.Repeat("{{message}}", store.AggregatePayloadByteLimit)
		}
		err := sender.Deliver(context.Background(), provider, config, notification)
		var result *DeliveryError
		if !errors.As(err, &result) || !result.Permanent || result.Ambiguous {
			t.Fatalf("local bound: %s %v", provider, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("oversized local payload performed I/O")
	}
}

func TestSMTPAggregateMIMEContent(t *testing.T) {
	n := Notification{Title: "MyProbe 故障持续汇总（10 个事件）😀", Message: "节点😀 <b>不解释为标记</b>\n." + strings.Repeat("界", 700) + "\nhttps://probe.example.com/admin?aggregate=one&snapshot=two"}
	raw, err := smtpMessage(ChannelConfig{SMTPFrom: "sender@example.com", SMTPTo: "recipient@example.com"}, n)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range raw {
		if b >= 128 {
			t.Fatal("MIME transport requires 8BITMIME")
		}
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatal("SMTP line exceeds RFC bound")
		}
	}
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	title, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if err != nil || title != n.Title {
		t.Fatalf("subject: %q %v", title, err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil || strings.ReplaceAll(string(body), "\r\n", "\n") != n.Message {
		t.Fatal("body text or link changed")
	}
	for _, title := range []string{"line\r\nBcc: private-marker", strings.Repeat("a", 990)} {
		_, err := smtpMessage(ChannelConfig{}, Notification{Title: title})
		var classified *DeliveryError
		if !errors.As(err, &classified) || !classified.Permanent || classified.Ambiguous || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("invalid header was not known unsent")
		}
	}
}
