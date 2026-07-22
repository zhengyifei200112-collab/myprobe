package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSenderWebhook(t *testing.T) {
	var received Notification
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	sender := NewHTTPSender(receiver.Client())
	notification := Notification{Title: "alert", Message: "node offline", State: "firing", Kind: "offline", NodeID: "node-1", Timestamp: time.Now().UTC()}
	if err := sender.Deliver(context.Background(), "webhook", ChannelConfig{URL: receiver.URL}, notification); err != nil {
		t.Fatal(err)
	}
	if received.Message != notification.Message || received.NodeID != notification.NodeID {
		t.Fatalf("received = %#v", received)
	}
}

func TestHTTPSenderTelegram(t *testing.T) {
	var path string
	var body map[string]any
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	sender := NewHTTPSender(receiver.Client())
	sender.telegramBaseURL = receiver.URL
	if err := sender.Deliver(context.Background(), "telegram", ChannelConfig{BotToken: "abc:123", ChatID: "-100"}, Notification{Title: "告警", Message: "测试"}); err != nil {
		t.Fatal(err)
	}
	if path != "/botabc:123/sendMessage" || body["chat_id"] != "-100" || !strings.Contains(body["text"].(string), "测试") {
		t.Fatalf("path = %q, body = %#v", path, body)
	}
}

func TestHTTPSenderRejectsFailureStatusAndInvalidURL(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusBadGateway) }))
	defer receiver.Close()
	sender := NewHTTPSender(receiver.Client())
	if err := sender.Deliver(context.Background(), "webhook", ChannelConfig{URL: receiver.URL}, Notification{}); err == nil {
		t.Fatal("non-success response was accepted")
	}
	if err := sender.Deliver(context.Background(), "webhook", ChannelConfig{URL: "file:///tmp/hook"}, Notification{}); err == nil {
		t.Fatal("non-HTTP URL was accepted")
	}
}
