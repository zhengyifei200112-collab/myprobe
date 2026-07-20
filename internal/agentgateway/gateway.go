package agentgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type Gateway struct {
	store *store.Store
	hub   *Hub
}

func New(database *store.Store, hub *Hub) *Gateway {
	return &Gateway{store: database, hub: hub}
}

func (g *Gateway) HTTPReport(w http.ResponseWriter, r *http.Request) {
	node, ok := g.authenticate(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxMessageBytes)
	defer r.Body.Close()
	var envelope protocol.Envelope
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON envelope"})
		return
	}
	if err := envelope.Validate(time.Now().UTC()); err != nil || envelope.Type != protocol.TypeReport {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report envelope"})
		return
	}
	report, err := protocol.DecodePayload[protocol.Report](envelope)
	if err != nil || report.Validate() != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid report payload"})
		return
	}
	if err := g.persistAndPublish(r.Context(), node, report); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store report"})
		return
	}
	writeJSON(w, http.StatusOK, protocol.Acknowledgement{Sequence: envelope.Sequence})
}

func (g *Gateway) WebSocket(w http.ResponseWriter, r *http.Request) {
	node, ok := g.authenticate(w, r)
	if !ok {
		return
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: false,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer connection.Close(websocket.StatusNormalClosure, "connection closed")
	connection.SetReadLimit(protocol.MaxMessageBytes)

	ctx := r.Context()
	firstCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	var first protocol.Envelope
	err = wsjson.Read(firstCtx, connection, &first)
	cancel()
	if err != nil || first.Validate(time.Now().UTC()) != nil || first.Type != protocol.TypeHello {
		_ = connection.Close(websocket.StatusPolicyViolation, "hello required")
		return
	}
	if _, err := protocol.DecodePayload[protocol.Hello](first); err != nil {
		_ = connection.Close(websocket.StatusPolicyViolation, "invalid hello")
		return
	}
	welcome, _ := protocol.NewEnvelope(protocol.TypeWelcome, 0, protocol.Welcome{
		ConnectionID: fmt.Sprintf("%s-%d", node.ID, time.Now().UnixNano()),
		ServerTime:   time.Now().UTC(),
		Config: protocol.Config{
			CollectionSeconds: node.CollectionSeconds,
			ReportSeconds:     node.ReportSeconds,
		},
	})
	if err := wsjson.Write(ctx, connection, welcome); err != nil {
		return
	}

	for {
		readCtx, readCancel := context.WithTimeout(ctx, 75*time.Second)
		var envelope protocol.Envelope
		err := wsjson.Read(readCtx, connection, &envelope)
		readCancel()
		if err != nil {
			return
		}
		if err := envelope.Validate(time.Now().UTC()); err != nil {
			g.writeProtocolError(ctx, connection, "invalid_envelope", err.Error())
			continue
		}
		switch envelope.Type {
		case protocol.TypeReport:
			report, err := protocol.DecodePayload[protocol.Report](envelope)
			if err != nil || report.Validate() != nil {
				g.writeProtocolError(ctx, connection, "invalid_report", "report validation failed")
				continue
			}
			if err := g.persistAndPublish(ctx, node, report); err != nil {
				g.writeProtocolError(ctx, connection, "storage_error", "report could not be stored")
				continue
			}
			ack, _ := protocol.NewEnvelope(protocol.TypeAcknowledged, envelope.Sequence, protocol.Acknowledgement{Sequence: envelope.Sequence})
			if err := wsjson.Write(ctx, connection, ack); err != nil {
				return
			}
		case protocol.TypeHeartbeat:
			ack, _ := protocol.NewEnvelope(protocol.TypeAcknowledged, envelope.Sequence, protocol.Acknowledgement{Sequence: envelope.Sequence})
			if err := wsjson.Write(ctx, connection, ack); err != nil {
				return
			}
		case protocol.TypePingResult, protocol.TypeTCPingResult:
			// Persistence for scheduled latency tasks is added with the scheduler milestone.
		default:
			g.writeProtocolError(ctx, connection, "unsupported_type", envelope.Type)
		}
	}
}

func (g *Gateway) persistAndPublish(ctx context.Context, node store.Node, report protocol.Report) error {
	if err := g.store.SaveReport(ctx, node.ID, report); err != nil {
		return err
	}
	items, err := g.store.ListPublicNodes(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Node.ID == node.ID {
			g.hub.Publish(Event{Type: "node_metrics", Node: item})
			break
		}
	}
	return nil
}

func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request) (store.Node, bool) {
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, "Bearer ") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bearer token required"})
		return store.Node{}, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))
	node, err := g.store.AuthenticateAgent(r.Context(), token)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid agent token"})
		return store.Node{}, false
	}
	return node, true
}

func (g *Gateway) writeProtocolError(ctx context.Context, connection *websocket.Conn, code, message string) {
	envelope, err := protocol.NewEnvelope(protocol.TypeError, 0, protocol.ProtocolError{Code: code, Message: message})
	if err == nil {
		_ = wsjson.Write(ctx, connection, envelope)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func IsNormalClose(err error) bool {
	return errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure
}
