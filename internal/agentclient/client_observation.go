package agentclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"net"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

// Private test observation seam. Install only before Run or other send methods;
// the callback is immutable while the Client is running. Production clients
// leave it nil. Events contain no credentials, addresses or payloads.
type clientEnvelopeEvent struct {
	Phase, Type, Transport, ErrorClass string
	Sequence                           uint64
	HTTPStatus                         int
	CapturedAt                         time.Time
	PayloadSHA256                      [32]byte
}

func (c *Client) nextSequence(kind string) uint64 {
	sequence := c.sequence.Add(1)
	c.observeClientEnvelope(clientEnvelopeEvent{Phase: "generated", Type: kind, Sequence: sequence})
	return sequence
}

func (c *Client) observeClientEnvelope(event clientEnvelopeEvent) {
	if c.observeEnvelope != nil {
		c.observeEnvelope(event)
	}
}

func (c *Client) observeEnvelopeReturn(kind string, sequence uint64, transport string, err error, status int) {
	if c.observeEnvelope == nil {
		return
	}
	class := ""
	if err != nil {
		var networkError net.Error
		switch {
		case errors.Is(err, context.Canceled):
			class = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			class = "deadline"
		case errors.As(err, &networkError) && networkError.Timeout():
			class = "network_timeout"
		default:
			class = "transport"
		}
	}
	// HTTP.Do returning a response is not application acceptance. Preserve status
	// independently, including4xx/5xx responses with no transport error.
	c.observeClientEnvelope(clientEnvelopeEvent{Phase: "write_returned", Type: kind, Sequence: sequence, Transport: transport, ErrorClass: class, HTTPStatus: status})
}

func (c *Client) observeEnvelopeRejection(kind string, sequence uint64, class string) {
	c.observeClientEnvelope(clientEnvelopeEvent{Phase: "local_rejected", Type: kind, Sequence: sequence, Transport: "local", ErrorClass: class})
}

func (c *Client) observeReportEnvelope(sequence uint64, captured time.Time, payload []byte) {
	if c.observeEnvelope == nil {
		return
	}
	c.observeClientEnvelope(clientEnvelopeEvent{Phase: "report", Type: protocol.TypeReport, Sequence: sequence, CapturedAt: captured, PayloadSHA256: sha256.Sum256(payload)})
}

func (c *Client) writeV1Envelope(ctx context.Context, connection *websocket.Conn, envelope protocol.Envelope) error {
	err := wsjson.Write(ctx, connection, envelope)
	c.observeEnvelopeReturn(envelope.Type, envelope.Sequence, "websocket", err, 0)
	return err
}

func (c *Client) writeV2Envelope(ctx context.Context, connection *websocket.Conn, envelope v2.Envelope) error {
	err := wsjson.Write(ctx, connection, envelope)
	c.observeEnvelopeReturn(envelope.Type, envelope.Sequence, "websocket", err, 0)
	return err
}
