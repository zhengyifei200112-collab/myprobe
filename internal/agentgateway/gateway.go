package agentgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type Gateway struct {
	store      *store.Store
	hub        *Hub
	sessionsMu sync.RWMutex
	sessions   map[string]*agentSession
	pendingMu  sync.Mutex
	pending    map[string]pendingTask
}

func New(database *store.Store, hub *Hub) *Gateway {
	return &Gateway{store: database, hub: hub, sessions: make(map[string]*agentSession), pending: make(map[string]pendingTask)}
}

var ErrAgentOffline = errors.New("agent is offline")

type agentSession struct {
	connection *websocket.Conn
	writeMu    sync.Mutex
}

func (s *agentSession) write(ctx context.Context, envelope protocol.Envelope) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return wsjson.Write(ctx, s.connection, envelope)
}

type pendingTask struct {
	nodeID   string
	targetID string
	kind     string
	expires  time.Time
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

func (g *Gateway) HTTPHello(w http.ResponseWriter, r *http.Request) {
	node, ok := g.authenticate(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, protocol.MaxMessageBytes)
	defer r.Body.Close()
	var envelope protocol.Envelope
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || envelope.Validate(time.Now().UTC()) != nil || envelope.Type != protocol.TypeHello {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid hello envelope"})
		return
	}
	hello, err := protocol.DecodePayload[protocol.Hello](envelope)
	if err != nil || hello.Validate() != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid hello payload"})
		return
	}
	if err := g.store.SaveAgentMetadata(r.Context(), node.ID, hello); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store agent metadata"})
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
	hello, err := protocol.DecodePayload[protocol.Hello](first)
	if err != nil || hello.Validate() != nil {
		_ = connection.Close(websocket.StatusPolicyViolation, "invalid hello")
		return
	}
	if err := g.store.SaveAgentMetadata(ctx, node.ID, hello); err != nil {
		_ = connection.Close(websocket.StatusInternalError, "metadata storage failed")
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
	session := &agentSession{connection: connection}
	// Publish the session while holding its writer lock. A scheduler can discover
	// it immediately, but its first task cannot overtake the welcome frame.
	session.writeMu.Lock()
	g.register(node.ID, session)
	err = wsjson.Write(ctx, connection, welcome)
	session.writeMu.Unlock()
	if err != nil {
		g.unregister(node.ID, session)
		return
	}
	defer g.unregister(node.ID, session)

	for {
		readCtx, readCancel := context.WithTimeout(ctx, 75*time.Second)
		var envelope protocol.Envelope
		err := wsjson.Read(readCtx, connection, &envelope)
		readCancel()
		if err != nil {
			return
		}
		if err := envelope.Validate(time.Now().UTC()); err != nil {
			g.writeProtocolError(ctx, session, "invalid_envelope", err.Error())
			continue
		}
		switch envelope.Type {
		case protocol.TypeReport:
			report, err := protocol.DecodePayload[protocol.Report](envelope)
			if err != nil || report.Validate() != nil {
				g.writeProtocolError(ctx, session, "invalid_report", "report validation failed")
				continue
			}
			if err := g.persistAndPublish(ctx, node, report); err != nil {
				g.writeProtocolError(ctx, session, "storage_error", "report could not be stored")
				continue
			}
			ack, _ := protocol.NewEnvelope(protocol.TypeAcknowledged, envelope.Sequence, protocol.Acknowledgement{Sequence: envelope.Sequence})
			if err := session.write(ctx, ack); err != nil {
				return
			}
		case protocol.TypeHeartbeat:
			ack, _ := protocol.NewEnvelope(protocol.TypeAcknowledged, envelope.Sequence, protocol.Acknowledgement{Sequence: envelope.Sequence})
			if err := session.write(ctx, ack); err != nil {
				return
			}
		case protocol.TypePingResult, protocol.TypeTCPingResult:
			result, err := protocol.DecodePayload[protocol.LatencyResult](envelope)
			kind := protocol.TaskKindPing
			if envelope.Type == protocol.TypeTCPingResult {
				kind = protocol.TaskKindTCPing
			}
			if err != nil || result.Validate(time.Now().UTC()) != nil || !g.consumePending(node.ID, kind, result) {
				g.writeProtocolError(ctx, session, "invalid_result", "latency result does not match an active task")
				continue
			}
			if err := g.store.SaveLatencyResult(ctx, node.ID, kind, result); err != nil {
				g.writeProtocolError(ctx, session, "storage_error", "latency result could not be stored")
				continue
			}
			g.publishNode(ctx, node.ID)
			ack, _ := protocol.NewEnvelope(protocol.TypeAcknowledged, envelope.Sequence, protocol.Acknowledgement{Sequence: envelope.Sequence})
			if err := session.write(ctx, ack); err != nil {
				return
			}
		default:
			g.writeProtocolError(ctx, session, "unsupported_type", envelope.Type)
		}
	}
}

func (g *Gateway) persistAndPublish(ctx context.Context, node store.Node, report protocol.Report) error {
	if err := g.store.SaveReport(ctx, node.ID, report); err != nil {
		return err
	}
	g.publishNode(ctx, node.ID)
	return nil
}

func (g *Gateway) publishNode(ctx context.Context, nodeID string) {
	items, err := g.store.ListPublicNodes(ctx, time.Now().UTC())
	if err != nil {
		return
	}
	for _, item := range items {
		if item.Node.ID == nodeID {
			g.hub.Publish(Event{Type: "node_metrics", Node: item})
			break
		}
	}
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

func (g *Gateway) writeProtocolError(ctx context.Context, session *agentSession, code, message string) {
	envelope, err := protocol.NewEnvelope(protocol.TypeError, 0, protocol.ProtocolError{Code: code, Message: message})
	if err == nil {
		_ = session.write(ctx, envelope)
	}
}

func (g *Gateway) SendTask(ctx context.Context, nodeID string, task protocol.Task) error {
	if err := task.Validate(time.Now().UTC()); err != nil {
		return err
	}
	g.sessionsMu.RLock()
	session := g.sessions[nodeID]
	g.sessionsMu.RUnlock()
	if session == nil {
		return ErrAgentOffline
	}
	envelope, err := protocol.NewEnvelope(protocol.TypeTask, 0, task)
	if err != nil {
		return err
	}
	g.pendingMu.Lock()
	for id, pending := range g.pending {
		if pending.expires.Before(time.Now().UTC()) {
			delete(g.pending, id)
		}
	}
	g.pending[task.ID] = pendingTask{nodeID: nodeID, targetID: task.TargetID, kind: task.Kind, expires: task.ExpiresAt}
	g.pendingMu.Unlock()
	if err := session.write(ctx, envelope); err != nil {
		g.pendingMu.Lock()
		delete(g.pending, task.ID)
		g.pendingMu.Unlock()
		return err
	}
	return nil
}

func (g *Gateway) consumePending(nodeID, kind string, result protocol.LatencyResult) bool {
	g.pendingMu.Lock()
	defer g.pendingMu.Unlock()
	pending, ok := g.pending[result.TaskID]
	if !ok || pending.nodeID != nodeID || pending.targetID != result.TargetID || pending.kind != kind || pending.expires.Before(time.Now().UTC()) {
		return false
	}
	delete(g.pending, result.TaskID)
	return true
}

func (g *Gateway) register(nodeID string, session *agentSession) {
	g.sessionsMu.Lock()
	previous := g.sessions[nodeID]
	g.sessions[nodeID] = session
	g.sessionsMu.Unlock()
	if previous != nil && previous != session {
		_ = previous.connection.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
}

func (g *Gateway) unregister(nodeID string, expected *agentSession) {
	g.sessionsMu.Lock()
	if g.sessions[nodeID] == expected {
		delete(g.sessions, nodeID)
	}
	g.sessionsMu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func IsNormalClose(err error) bool {
	return errors.Is(err, context.Canceled) || websocket.CloseStatus(err) == websocket.StatusNormalClosure
}
