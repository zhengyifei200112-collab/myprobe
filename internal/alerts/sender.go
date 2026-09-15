package alerts

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ChannelConfig struct {
	URL            string            `json:"url,omitempty"`
	Method         string            `json:"method,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	BodyTemplate   string            `json:"body_template,omitempty"`
	BotToken       string            `json:"bot_token,omitempty"`
	ChatID         string            `json:"chat_id,omitempty"`
	ThreadID       string            `json:"thread_id,omitempty"`
	ParseMode      string            `json:"parse_mode,omitempty"`
	SMTPHost       string            `json:"smtp_host,omitempty"`
	SMTPPort       int               `json:"smtp_port,omitempty"`
	SMTPUsername   string            `json:"smtp_username,omitempty"`
	SMTPPassword   string            `json:"smtp_password,omitempty"`
	SMTPFrom       string            `json:"smtp_from,omitempty"`
	SMTPTo         string            `json:"smtp_to,omitempty"`
	SMTPEncryption string            `json:"smtp_encryption,omitempty"`
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
	allowPrivate    bool
}

func NewHTTPSender(client *http.Client) *HTTPSender {
	allowPrivate := client != nil
	if client == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second}
		transport := &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("invalid notification destination")
			}
			ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("notification destination could not be resolved")
			}
			for _, ip := range ips {
				if !isPrivateIP(ip) {
					return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				}
			}
			return nil, errors.New("notification destination is not publicly routable")
		}}
		client = &http.Client{Timeout: 10 * time.Second, Transport: transport}
	}
	return &HTTPSender{client: client, telegramBaseURL: "https://api.telegram.org", allowPrivate: allowPrivate}
}

func (s *HTTPSender) Deliver(ctx context.Context, kind string, config ChannelConfig, notification Notification) error {
	switch kind {
	case "webhook":
		return s.webhook(ctx, config, notification)
	case "telegram":
		return s.telegram(ctx, config, notification)
	case "discord":
		return s.discord(ctx, config, notification)
	case "smtp":
		return s.smtp(ctx, config, notification)
	default:
		return errors.New("unsupported notification channel")
	}
}

func (s *HTTPSender) webhook(ctx context.Context, config ChannelConfig, notification Notification) error {
	if err := validateChannelConfig("webhook", config); err != nil {
		return err
	}
	if !s.allowPrivate {
		if err := validatePublicEndpoint(config.URL); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(notification)
	if config.BodyTemplate != "" {
		body = []byte(renderTemplate(config.BodyTemplate, notification))
	}
	method := strings.ToUpper(strings.TrimSpace(config.Method))
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, config.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "MyProbe/1 notification")
	for key, value := range config.Headers {
		if strings.EqualFold(key, "Host") || strings.ContainsAny(key+value, "\r\n") {
			return errors.New("invalid webhook header")
		}
		request.Header.Set(key, value)
	}
	return s.do(request)
}

func (s *HTTPSender) discord(ctx context.Context, config ChannelConfig, notification Notification) error {
	if err := validateChannelConfig("discord", config); err != nil {
		return err
	}
	if !s.allowPrivate && !strings.HasPrefix(strings.ToLower(config.URL), "https://") {
		return errors.New("Discord webhook must use HTTPS")
	}
	if !s.allowPrivate {
		if err := validatePublicEndpoint(config.URL); err != nil {
			return err
		}
	}
	body, _ := json.Marshal(map[string]any{"content": "**" + notification.Title + "**\n" + notification.Message, "allowed_mentions": map[string]any{"parse": []string{}}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "MyProbe/1 notification")
	return s.do(req)
}

func (s *HTTPSender) telegram(ctx context.Context, config ChannelConfig, notification Notification) error {
	if err := validateChannelConfig("telegram", config); err != nil {
		return err
	}
	endpoint := strings.TrimRight(s.telegramBaseURL, "/") + "/bot" + url.PathEscape(config.BotToken) + "/sendMessage"
	payload := map[string]any{
		"chat_id": config.ChatID,
		"text":    notification.Title + "\n" + notification.Message,
	}
	if config.ThreadID != "" {
		threadID, err := strconv.Atoi(config.ThreadID)
		if err != nil {
			return errors.New("Telegram thread ID must be numeric")
		}
		payload["message_thread_id"] = threadID
	}
	if config.ParseMode != "" {
		payload["parse_mode"] = config.ParseMode
	}
	body, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "MyProbe/1 notification")
	return s.do(request)
}

func (s *HTTPSender) smtp(ctx context.Context, config ChannelConfig, notification Notification) error {
	if err := validateChannelConfig("smtp", config); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	address := net.JoinHostPort(config.SMTPHost, strconv.Itoa(config.SMTPPort))
	message := []byte("To: " + config.SMTPTo + "\r\nFrom: " + config.SMTPFrom + "\r\nSubject: " + notification.Title + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + notification.Message)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if config.SMTPEncryption == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: config.SMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return errors.New("SMTP connection failed")
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	client, err := smtp.NewClient(conn, config.SMTPHost)
	if err != nil {
		return errors.New("SMTP handshake failed")
	}
	defer client.Close()
	if config.SMTPEncryption == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: config.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return errors.New("SMTP TLS negotiation failed")
		}
	}
	if config.SMTPUsername != "" {
		if err := client.Auth(smtp.PlainAuth("", config.SMTPUsername, config.SMTPPassword, config.SMTPHost)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err := client.Mail(config.SMTPFrom); err != nil {
		return errors.New("SMTP sender rejected")
	}
	if err := client.Rcpt(config.SMTPTo); err != nil {
		return errors.New("SMTP recipient rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP message rejected")
	}
	if _, err = writer.Write(message); err != nil {
		return errors.New("SMTP delivery failed")
	}
	if err = writer.Close(); err != nil {
		return errors.New("SMTP delivery failed")
	}
	return client.Quit()
}

func (s *HTTPSender) do(request *http.Request) error {
	response, err := s.client.Do(request)
	if err != nil {
		return errors.New("notification delivery failed")
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
		method := strings.ToUpper(strings.TrimSpace(config.Method))
		if method != "" && method != "POST" && method != "PUT" && method != "PATCH" {
			return errors.New("webhook method must be POST, PUT, or PATCH")
		}
	case "discord":
		parsed, err := url.Parse(strings.TrimSpace(config.URL))
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
			return errors.New("Discord webhook must be an absolute HTTP(S) URL")
		}
	case "telegram":
		if strings.TrimSpace(config.BotToken) == "" || strings.TrimSpace(config.ChatID) == "" || strings.ContainsAny(config.BotToken, "\r\n/") {
			return errors.New("Telegram bot token and chat ID are required")
		}
		if config.ParseMode != "" && config.ParseMode != "HTML" && config.ParseMode != "MarkdownV2" {
			return errors.New("unsupported Telegram parse mode")
		}
	case "smtp":
		if strings.TrimSpace(config.SMTPHost) == "" || config.SMTPPort < 1 || config.SMTPPort > 65535 || !strings.Contains(config.SMTPFrom, "@") || !strings.Contains(config.SMTPTo, "@") || strings.ContainsAny(config.SMTPHost+config.SMTPFrom+config.SMTPTo, "\r\n") {
			return errors.New("valid SMTP host, port, sender, and recipient are required")
		}
		if config.SMTPEncryption != "starttls" && config.SMTPEncryption != "tls" {
			return errors.New("SMTP encryption must be STARTTLS or TLS")
		}
	default:
		return errors.New("unsupported notification channel")
	}
	return nil
}

func validatePublicEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New("invalid webhook URL")
	}
	host := parsed.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		return errors.New("webhook host could not be resolved")
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return errors.New("webhook destination is not publicly routable")
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

func renderTemplate(value string, n Notification) string {
	replacer := strings.NewReplacer("{{title}}", n.Title, "{{message}}", n.Message, "{{state}}", n.State, "{{event}}", n.Kind, "{{event.type}}", n.Kind, "{{event.status}}", n.State, "{{event.time}}", n.Timestamp.Local().Format("2006-01-02 15:04:05"), "{{node.id}}", n.NodeID, "{{node.name}}", n.NodeName, "{{rule.id}}", n.RuleID)
	return replacer.Replace(value)
}
