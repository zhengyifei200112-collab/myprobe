package agentclient

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type Config struct {
	ServerURL        string
	Token            string
	CollectionPeriod time.Duration
	ReportPeriod     time.Duration
	AgentVersion     string
}

type Client struct {
	config    Config
	baseURL   *url.URL
	collector *collector.Collector
	logger    *slog.Logger
	http      *http.Client
	sequence  atomic.Uint64
	reportNS  atomic.Int64
	connMu    sync.RWMutex
	writeMu   sync.Mutex
	conn      *websocket.Conn
	taskSlots chan struct{}
}

func New(config Config, source *collector.Collector, logger *slog.Logger) (*Client, error) {
	parsed, err := url.Parse(config.ServerURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("server URL must use http or https and include a host")
	}
	if strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("agent token is required")
	}
	if config.CollectionPeriod < time.Second || config.ReportPeriod < time.Second {
		return nil, errors.New("collection and report periods must be at least one second")
	}
	client := &Client{
		config: config, baseURL: parsed, collector: source, logger: logger,
		http:      &http.Client{Timeout: 15 * time.Second},
		taskSlots: make(chan struct{}, 8),
	}
	client.reportNS.Store(int64(config.ReportPeriod))
	return client, nil
}

func (c *Client) Run(ctx context.Context) error {
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		c.connectionLoop(ctx)
	}()
	go func() {
		defer wait.Done()
		c.reportLoop(ctx)
	}()
	<-ctx.Done()
	c.clearConnection(nil)
	wait.Wait()
	return nil
}

func (c *Client) connectionLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		connected, err := c.connectAndRead(ctx)
		if connected {
			// A completed handshake proves the server is reachable. A later drop
			// starts a fresh reconnect cycle instead of inheriting old failures.
			backoff = time.Second
		}
		if err != nil && ctx.Err() == nil {
			c.logger.Warn("agent websocket disconnected", "error", err, "retry_in", backoff)
		}
		delay := withJitter(backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if backoff < time.Minute {
			backoff *= 2
			if backoff > time.Minute {
				backoff = time.Minute
			}
		}
	}
}

func (c *Client) connectAndRead(ctx context.Context) (bool, error) {
	wsURL := *c.baseURL
	if wsURL.Scheme == "https" {
		wsURL.Scheme = "wss"
	} else {
		wsURL.Scheme = "ws"
	}
	wsURL.Path = path.Join(wsURL.Path, "/api/v1/agent/ws")
	header := http.Header{"Authorization": []string{"Bearer " + c.config.Token}}
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	connection, response, err := websocket.Dial(dialCtx, wsURL.String(), &websocket.DialOptions{HTTPHeader: header, CompressionMode: websocket.CompressionDisabled})
	cancel()
	if err != nil {
		return false, err
	}
	negotiatedExtensions := response.Header.Get("Sec-WebSocket-Extensions")
	connection.SetReadLimit(protocol.MaxMessageBytes)

	hello, err := c.collector.Hello(ctx, c.config.AgentVersion, int(c.config.CollectionPeriod/time.Second), int(c.config.ReportPeriod/time.Second))
	if err != nil {
		connection.Close(websocket.StatusInternalError, "host discovery failed")
		return false, err
	}
	envelope, err := protocol.NewEnvelope(protocol.TypeHello, c.sequence.Add(1), hello)
	if err != nil || wsjson.Write(ctx, connection, envelope) != nil {
		connection.Close(websocket.StatusInternalError, "hello failed")
		return false, errors.New("send hello")
	}
	var welcome protocol.Envelope
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(readCtx, connection, &welcome)
	readCancel()
	if err != nil || welcome.Type != protocol.TypeWelcome {
		connection.Close(websocket.StatusPolicyViolation, "welcome required")
		return false, fmt.Errorf("server did not send welcome: read_error=%v message_type=%q extensions=%q", err, welcome.Type, negotiatedExtensions)
	}
	c.replaceConnection(connection)
	c.logger.Info("agent websocket connected", "server", c.baseURL.Host)
	connectionCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go c.heartbeatLoop(connectionCtx, connection)

	for ctx.Err() == nil {
		var message protocol.Envelope
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			c.clearConnection(connection)
			return true, err
		}
		switch message.Type {
		case protocol.TypeConfiguration:
			config, err := protocol.DecodePayload[protocol.Config](message)
			if err == nil {
				c.applyConfig(config)
			}
		case protocol.TypeTask:
			task, err := protocol.DecodePayload[protocol.Task](message)
			if err == nil && task.Validate(time.Now().UTC()) == nil {
				c.startTask(ctx, task)
			}
		case protocol.TypeAcknowledged:
		default:
			c.logger.Debug("ignored server message", "type", message.Type)
		}
	}
	return true, ctx.Err()
}

func (c *Client) heartbeatLoop(ctx context.Context, expected *websocket.Conn) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			envelope, err := protocol.NewEnvelope(protocol.TypeHeartbeat, c.sequence.Add(1), struct{}{})
			if err != nil {
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			c.writeMu.Lock()
			err = wsjson.Write(writeCtx, expected, envelope)
			c.writeMu.Unlock()
			cancel()
			if err != nil {
				c.clearConnection(expected)
				return
			}
		}
	}
}

func (c *Client) startTask(ctx context.Context, task protocol.Task) {
	select {
	case c.taskSlots <- struct{}{}:
		go func() {
			defer func() { <-c.taskSlots }()
			result := executeTask(ctx, task)
			if err := c.sendLatencyResult(ctx, task.Kind, result); err != nil && ctx.Err() == nil {
				c.logger.Warn("upload latency result", "kind", task.Kind, "target_id", task.TargetID, "error", err)
			}
		}()
	default:
		result := protocol.LatencyResult{TaskID: task.ID, TargetID: task.TargetID, ErrorClass: "busy", CompletedAt: time.Now().UTC()}
		if err := c.sendLatencyResult(ctx, task.Kind, result); err != nil {
			c.logger.Warn("upload busy latency result", "target_id", task.TargetID, "error", err)
		}
	}
}

func (c *Client) sendLatencyResult(ctx context.Context, kind string, result protocol.LatencyResult) error {
	messageType := protocol.TypePingResult
	if kind == protocol.TaskKindTCPing {
		messageType = protocol.TypeTCPingResult
	}
	envelope, err := protocol.NewEnvelope(messageType, c.sequence.Add(1), result)
	if err != nil {
		return err
	}
	connection := c.currentConnection()
	if connection == nil {
		return errors.New("agent websocket is disconnected")
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.writeMu.Lock()
	err = wsjson.Write(writeCtx, connection, envelope)
	c.writeMu.Unlock()
	if err != nil {
		c.clearConnection(connection)
	}
	return err
}

func (c *Client) reportLoop(ctx context.Context) {
	for ctx.Err() == nil {
		period := time.Duration(c.reportNS.Load())
		timer := time.NewTimer(period)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		collectCtx, cancel := context.WithTimeout(ctx, min(period, 15*time.Second))
		report, err := c.collector.Collect(collectCtx)
		cancel()
		if err != nil {
			c.logger.Warn("collect metrics", "error", err)
			continue
		}
		if err := c.sendReport(ctx, report); err != nil {
			c.logger.Warn("upload report", "error", err)
		}
	}
}

func (c *Client) sendReport(ctx context.Context, report protocol.Report) error {
	sequence := c.sequence.Add(1)
	envelope, err := protocol.NewEnvelope(protocol.TypeReport, sequence, report)
	if err != nil {
		return err
	}
	if connection := c.currentConnection(); connection != nil {
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		c.writeMu.Lock()
		err = wsjson.Write(writeCtx, connection, envelope)
		c.writeMu.Unlock()
		cancel()
		if err == nil {
			return nil
		}
		c.clearConnection(connection)
	}
	return c.sendHTTP(ctx, envelope)
}

func (c *Client) sendHTTP(ctx context.Context, envelope protocol.Envelope) error {
	endpoint := *c.baseURL
	endpoint.Path = path.Join(endpoint.Path, "/api/v1/agent/report")
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP fallback status %d", response.StatusCode)
	}
	return nil
}

func (c *Client) applyConfig(config protocol.Config) {
	if config.ReportSeconds >= 1 && config.ReportSeconds <= 3600 {
		c.reportNS.Store(int64(time.Duration(config.ReportSeconds) * time.Second))
	}
	c.collector.UpdateConfig(collector.Config{Interfaces: config.Interfaces, Mounts: config.Mounts})
}

func (c *Client) currentConnection() *websocket.Conn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *Client) replaceConnection(connection *websocket.Conn) {
	c.connMu.Lock()
	old := c.conn
	c.conn = connection
	c.connMu.Unlock()
	if old != nil && old != connection {
		_ = old.Close(websocket.StatusNormalClosure, "replaced")
	}
}

func (c *Client) clearConnection(expected *websocket.Conn) {
	c.connMu.Lock()
	if expected == nil || c.conn == expected {
		old := c.conn
		c.conn = nil
		c.connMu.Unlock()
		if old != nil {
			_ = old.Close(websocket.StatusNormalClosure, "reconnecting")
		}
		return
	}
	c.connMu.Unlock()
}

func withJitter(base time.Duration) time.Duration {
	var value uint64
	if err := binary.Read(rand.Reader, binary.LittleEndian, &value); err != nil {
		return base
	}
	// 80%-120% jitter prevents synchronized reconnect storms.
	percent := 80 + value%41
	return time.Duration(int64(base) * int64(percent) / 100)
}
