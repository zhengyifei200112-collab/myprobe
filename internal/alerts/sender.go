package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type ChannelConfig struct {
	URL      string `json:"url,omitempty"`
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
}

type Notification struct {
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	State     string    `json:"state"`
	Kind      string    `json:"kind"`
	NodeID    string    `json:"node_id"`
	NodeName  string    `json:"node_name"`
	RuleID    string    `json:"rule_id"`
	Timestamp time.Time `json:"timestamp"`
}

type Sender interface {
	Deliver(context.Context, string, ChannelConfig, Notification) error
}

type HTTPSender struct {
	client          *http.Client
	telegramBaseURL string
}

func NewHTTPSender(client *http.Client) *HTTPSender {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPSender{client: client, telegramBaseURL: "https://api.telegram.org"}
}

func (s *HTTPSender) Deliver(ctx context.Context, kind string, config ChannelConfig, notification Notification) error {
	switch kind {
	case "webhook":
		return s.webhook(ctx, config, notification)
	case "telegram":
		return s.telegram(ctx, config, notification)
	default:
		return errors.New("unsupported notification channel")
	}
}

func (s *HTTPSender) webhook(ctx context.Context, config ChannelConfig, notification Notification) error {
	if err := validateChannelConfig("webhook", config); err != nil {
		return err
	}
	body, _ := json.Marshal(notification)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "MyProbe/1 notification")
	return s.do(request)
}

func (s *HTTPSender) telegram(ctx context.Context, config ChannelConfig, notification Notification) error {
	if err := validateChannelConfig("telegram", config); err != nil {
		return err
	}
	endpoint := strings.TrimRight(s.telegramBaseURL, "/") + "/bot" + url.PathEscape(config.BotToken) + "/sendMessage"
	body, _ := json.Marshal(map[string]any{
		"chat_id": config.ChatID,
		"text":    notification.Title + "\n" + notification.Message,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "MyProbe/1 notification")
	return s.do(request)
}

func (s *HTTPSender) do(request *http.Request) error {
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("notification receiver returned HTTP %d", response.StatusCode)
	}
	return nil
}

func validateChannelConfig(kind string, config ChannelConfig) error {
	switch kind {
	case "webhook":
		parsed, err := url.Parse(strings.TrimSpace(config.URL))
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
			return errors.New("webhook URL must be an absolute HTTP(S) URL without user information")
		}
	case "telegram":
		if strings.TrimSpace(config.BotToken) == "" || strings.TrimSpace(config.ChatID) == "" || strings.ContainsAny(config.BotToken, "\r\n/") {
			return errors.New("Telegram bot token and chat ID are required")
		}
	default:
		return errors.New("unsupported notification channel")
	}
	return nil
}
