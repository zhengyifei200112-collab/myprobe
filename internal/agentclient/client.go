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
	"github.com/zhengyifei200112-collab/myprobe/internal/httpprobe"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	protocolv2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

type Config struct {
	ServerURL           string
	Token               string
	CollectionPeriod    time.Duration
	ReportPeriod        time.Duration
	AgentVersion        string
	HTTPPrivateCIDRs    []string
	HTTPAdditionalPorts []int
}

type Client struct {
	config            Config
	baseURL           *url.URL
	collector         metricsSource
	logger            *slog.Logger
	http              *http.Client
	sequence          atomic.Uint64
	reportNS          atomic.Int64
	collectionNS      atomic.Int64
	settingsMu        sync.Mutex
	sampleMu          sync.Mutex
	sample            *collectedSample
	sampleGeneration  atomic.Uint64
	sampleSequence    uint64
	collectionChanged chan struct{}
	reportChanged     chan struct{}
	connMu            sync.RWMutex
	writeMu           sync.Mutex
	conn              *websocket.Conn
	connVersion       int
	taskSlots         chan struct{}
	httpTaskSlots     chan struct{}
	httpExecutor      httpExecutor
	observeEnvelope   func(clientEnvelopeEvent)
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
	if source == nil {
		return nil, errors.New("metric collector is required")
	}
	policy, err := httpprobe.NewPolicy(config.HTTPPrivateCIDRs, config.HTTPAdditionalPorts)
	if err != nil {
		return nil, err
	}
	client := &Client{
		httpTaskSlots: make(chan struct{}, 4), httpExecutor: httpprobe.New(policy),
		config: config, baseURL: parsed, collector: source, logger: logger,
		http:      &http.Client{Timeout: 15 * time.Second},
		taskSlots: make(chan struct{}, 8),
	}
	client.reportNS.Store(int64(config.ReportPeriod))
	client.collectionNS.Store(int64(config.CollectionPeriod))
	client.collectionChanged = make(chan struct{}, 1)
	client.reportChanged = make(chan struct{}, 1)
	return client, nil
}

func (c *Client) Run(ctx context.Context) error {
	var wait sync.WaitGroup
	wait.Add(4)
	go func() { defer wait.Done(); c.collectionLoop(ctx) }()
	go func() {
		defer wait.Done()
		c.connectionLoop(ctx)
	}()
	go func() {
		defer wait.Done()
		c.reportLoop(ctx)
	}()
	go func() { defer wait.Done(); c.fallbackContactLoop(ctx) }()
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
			if metadataErr := c.sendHelloHTTP(ctx); metadataErr != nil {
				c.logger.Debug("upload agent metadata over HTTP fallback", "error", metadataErr)
			}
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
	connection, response, version, err := c.dialPreferred(ctx)
	if err != nil {
		return false, err
	}
	negotiatedExtensions := response.Header.Get("Sec-WebSocket-Extensions")
	connection.SetReadLimit(protocol.MaxMessageBytes)

	hello, err := c.collector.Hello(ctx, c.config.AgentVersion, int(time.Duration(c.collectionNS.Load())/time.Second), int(time.Duration(c.reportNS.Load())/time.Second))
	if err != nil {
		connection.Close(websocket.StatusInternalError, "host discovery failed")
		return false, err
	}
	sequence := c.nextSequence(protocol.TypeHello)
	envelope, err := protocol.NewEnvelope(protocol.TypeHello, sequence, hello)
	if version == 2 {
		hello.Capabilities = append(hello.Capabilities, httpcheck.Capability, protocolv2.ConfigurationCapability, networkcheck.Capability)
		envelope, err = protocol.NewEnvelope(protocol.TypeHello, envelope.Sequence, hello)
	}
	envelope.Version = version
	if err != nil {
		c.observeEnvelopeRejection(protocol.TypeHello, sequence, "encoding")
	}
	if err != nil || c.writeV1Envelope(ctx, connection, envelope) != nil {
		connection.Close(websocket.StatusInternalError, "hello failed")
		return false, errors.New("send hello")
	}
	var welcome protocol.Envelope
	readCtx, readCancel := context.WithTimeout(ctx, 10*time.Second)
	err = wsjson.Read(readCtx, connection, &welcome)
	readCancel()
	if err != nil || welcome.Type != protocol.TypeWelcome || validateServerEnvelope(welcome, version) != nil {
		connection.Close(websocket.StatusPolicyViolation, "welcome required")
		return false, fmt.Errorf("server did not send welcome: read_error=%v message_type=%q extensions=%q", err, welcome.Type, negotiatedExtensions)
	}
	httpAllowed := false
	networkAllowed := false
	configurationAllowed := false
	var managedConfiguration *protocolv2.RuntimeConfiguration
	baseWelcome, baseDecodeErr := protocol.DecodePayload[protocol.Welcome](welcome)
	if baseDecodeErr != nil {
		connection.CloseNow()
		return false, errors.New("invalid welcome sampling configuration")
	}
	if version == 2 {
		negotiated, decodeErr := protocol.DecodePayload[protocolv2.Welcome](welcome)
		if decodeErr != nil {
			connection.CloseNow()
			return false, errors.New("invalid v2 welcome")
		}
		httpAllowed = protocolv2.AllowsHTTP(protocolv2.NegotiateCapabilities(hello.Capabilities, negotiated.Capabilities))
		networkAllowed = protocolv2.AllowsNetwork(protocolv2.NegotiateCapabilities(hello.Capabilities, negotiated.Capabilities))
		configurationAllowed = protocolv2.AllowsConfiguration(protocolv2.NegotiateCapabilities(hello.Capabilities, negotiated.Capabilities))
		if configurationAllowed {
			managedConfiguration = negotiated.RuntimeConfig
			if managedConfiguration == nil || managedConfiguration.Validate() != nil {
				connection.CloseNow()
				return false, errors.New("invalid negotiated runtime configuration")
			}
		}
	}
	// Validate the negotiated message before changing any local setting. The
	// managed extension is authoritative; its legacy copy can be an older read.
	var samplingErr error
	if managedConfiguration != nil {
		samplingErr = c.applySamplingPeriods(managedConfiguration.CollectionSeconds, managedConfiguration.ReportSeconds)
	} else {
		samplingErr = c.applyLegacySampling(baseWelcome.Config)
	}
	if samplingErr != nil {
		connection.CloseNow()
		return false, errors.New("invalid welcome sampling configuration")
	}
	c.replaceConnectionVersion(connection, version)
	defer c.clearConnection(connection)
	c.logger.Info("agent websocket connected", "server", c.baseURL.Host)
	connectionCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go c.heartbeatLoop(connectionCtx, connection, version)
	if managedConfiguration != nil {
		if err := c.sendConfigurationApplied(connectionCtx, connection, version, *managedConfiguration); err != nil {
			return true, err
		}
	}

	for ctx.Err() == nil {
		var message protocol.Envelope
		if err := wsjson.Read(ctx, connection, &message); err != nil {
			c.clearConnection(connection)
			return true, err
		}
		if err := validateServerEnvelope(message, version); err != nil {
			c.clearConnection(connection)
			return true, err
		}
		switch message.Type {
		case protocolv2.TypeNetworkTask:
			if !networkAllowed {
				continue
			}
			task, err := networkcheck.DecodeTask(message.Payload, time.Now().UTC())
			if err == nil {
				c.startNetworkTask(connectionCtx, connection, task)
			}
		case protocolv2.TypeHTTPTask:
			if !httpAllowed {
				continue
			}
			task, err := protocol.DecodePayload[httpcheck.Task](message)
			if err == nil && task.Validate(time.Now().UTC()) == nil {
				c.startHTTPTask(connectionCtx, connection, task)
			}
		case protocol.TypeConfiguration:
			if configurationAllowed {
				managed, decodeErr := protocol.DecodePayload[protocolv2.RuntimeConfiguration](message)
				if decodeErr != nil || managed.Validate() != nil || c.applySamplingPeriods(managed.CollectionSeconds, managed.ReportSeconds) != nil {
					continue
				}
				if err := c.sendConfigurationApplied(connectionCtx, connection, version, managed); err != nil {
					return true, err
				}
				continue
			}
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

func (c *Client) heartbeatLoop(ctx context.Context, expected *websocket.Conn, version int) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sequence := c.nextSequence(protocol.TypeHeartbeat)
			envelope, err := protocol.NewEnvelope(protocol.TypeHeartbeat, sequence, struct{}{})
			envelope.Version = version
			if err != nil {
				c.observeEnvelopeRejection(protocol.TypeHeartbeat, sequence, "encoding")
				continue
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			c.writeMu.Lock()
			err = c.writeV1Envelope(writeCtx, expected, envelope)
			c.writeMu.Unlock()
			cancel()
			if err != nil {
				c.clearConnection(expected)
				return
			}
		}
	}
}

// HTTP-only transport still needs contact when a long collection period leaves
// no new generation to report. Reuse the existing authenticated v1 hello rather
// than manufacture metric samples or introduce an unnegotiated wire extension.
func (c *Client) fallbackContactLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.currentConnection() != nil {
				continue
			}
			operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.sendHelloHTTP(operationCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				c.logger.Debug("HTTP contact unavailable", "error", err)
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
	sequence := c.nextSequence(messageType)
	envelope, err := protocol.NewEnvelope(messageType, sequence, result)
	if err != nil {
		c.observeEnvelopeRejection(messageType, sequence, "encoding")
		return err
	}
	connection, version := c.currentConnectionVersion()
	envelope.Version = version
	if connection == nil {
		c.observeEnvelopeRejection(envelope.Type, envelope.Sequence, "disconnected")
		return errors.New("agent websocket is disconnected")
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.writeMu.Lock()
	err = c.writeV1Envelope(writeCtx, connection, envelope)
	c.writeMu.Unlock()
	if err != nil {
		c.clearConnection(connection)
	}
	return err
}

func (c *Client) reportLoop(ctx context.Context) {
	var lastReported uint64
	for ctx.Err() == nil {
		period := time.Duration(c.reportNS.Load())
		timer := time.NewTimer(period)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-c.reportChanged:
			timer.Stop()
			continue
		}
		c.settingsMu.Lock()
		sample, ok := c.currentSample()
		if !ok || sample.id == lastReported {
			c.settingsMu.Unlock()
			continue
		}
		if err := c.sendReport(ctx, sample.report); err != nil {
			c.logger.Warn("upload report", "error", err)
		} else {
			lastReported = sample.id
		}
		c.settingsMu.Unlock()
	}
}

func (c *Client) sendReport(ctx context.Context, report protocol.Report) error {
	sequence := c.nextSequence(protocol.TypeReport)
	envelope, err := protocol.NewEnvelope(protocol.TypeReport, sequence, report)
	if err != nil {
		c.observeEnvelopeRejection(protocol.TypeReport, sequence, "encoding")
		return err
	}
	c.observeReportEnvelope(sequence, report.CapturedAt, envelope.Payload)
	if connection, version := c.currentConnectionVersion(); connection != nil {
		wireEnvelope := envelope
		wireEnvelope.Version = version
		writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		c.writeMu.Lock()
		err = c.writeV1Envelope(writeCtx, connection, wireEnvelope)
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
	endpointPath := "/api/v1/agent/report"
	if envelope.Type == protocol.TypeHello {
		endpointPath = "/api/v1/agent/hello"
	}
	endpoint.Path = path.Join(endpoint.Path, endpointPath)
	body, err := json.Marshal(envelope)
	if err != nil {
		c.observeEnvelopeRejection(envelope.Type, envelope.Sequence, "encoding")
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(string(body)))
	if err != nil {
		c.observeEnvelopeRejection(envelope.Type, envelope.Sequence, "request")
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.config.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	c.observeEnvelopeReturn(envelope.Type, envelope.Sequence, "http", err, status)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP fallback status %d", response.StatusCode)
	}
	return nil
}

func (c *Client) sendHelloHTTP(ctx context.Context) error {
	hello, err := c.collector.Hello(ctx, c.config.AgentVersion, int(time.Duration(c.collectionNS.Load())/time.Second), int(time.Duration(c.reportNS.Load())/time.Second))
	if err != nil {
		return err
	}
	sequence := c.nextSequence(protocol.TypeHello)
	envelope, err := protocol.NewEnvelope(protocol.TypeHello, sequence, hello)
	if err != nil {
		c.observeEnvelopeRejection(protocol.TypeHello, sequence, "encoding")
		return err
	}
	return c.sendHTTP(ctx, envelope)
}

func (c *Client) applyConfig(config protocol.Config) {
	if c.applyLegacySampling(config) != nil {
		return
	}
	if config.Interfaces != nil || config.Mounts != nil {
		c.collector.UpdateConfig(collector.Config{Interfaces: config.Interfaces, Mounts: config.Mounts})
	}
}

func (c *Client) currentConnection() *websocket.Conn {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn
}

func (c *Client) replaceConnection(connection *websocket.Conn) {
	c.replaceConnectionVersion(connection, protocol.Version)
}

func (c *Client) currentConnectionVersion() (*websocket.Conn, int) {
	c.connMu.RLock()
	defer c.connMu.RUnlock()
	return c.conn, c.connVersion
}

func (c *Client) replaceConnectionVersion(connection *websocket.Conn, version int) {
	c.connMu.Lock()
	old := c.conn
	c.conn = connection
	c.connVersion = version
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
