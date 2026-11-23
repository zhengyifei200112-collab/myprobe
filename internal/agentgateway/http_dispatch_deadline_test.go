package agentgateway

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHTTPDispatchAfterSQLiteWait(t *testing.T) {
	output := networkFixtureDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	databasePath := filepath.Join(output, "fixture.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic dispatch"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := database.SaveHTTPService(ctx, store.HTTPService{
		Name: "synthetic dispatch", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID},
		Spec: httpcheck.Spec{URL: "http://example.test/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 100, MaxBodyBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}

	accepted := make(chan *websocket.Conn, 1)
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
		<-stop
		conn.CloseNow()
	}))
	defer server.Close()
	defer close(stop)
	client, _, err := websocket.Dial(ctx, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseNow()
	session := &agentSession{connection: <-accepted, version: 2, httpAllowed: true, httpPending: make(map[string]time.Time)}
	gateway := New(database, NewHub())
	gateway.EnableHTTPProbes(true)
	gateway.sessions[node.ID] = session

	blocker, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	tx, err := blocker.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE http_services SET name=name WHERE id=?", service.ID); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	done := make(chan error, 1)
	go func() { done <- gateway.DispatchHTTP(ctx, node.ID, service.ID, started) }()
	time.Sleep(600 * time.Millisecond)
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	dispatchErr := <-done
	elapsed := time.Since(started)

	var expiry string
	var tasks, results int
	if err := blocker.QueryRowContext(ctx, "SELECT count(*),count(result_json),max(expires_at) FROM http_tasks").Scan(&tasks, &results, &expiry); err != nil {
		t.Fatal(err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expiry)
	if err != nil {
		t.Fatal(err)
	}
	var received protocol.Envelope
	var invalidTask bool
	if dispatchErr == nil {
		if err := wsjson.Read(ctx, client, &received); err != nil {
			t.Fatal(err)
		}
		task, err := protocol.DecodePayload[httpcheck.Task](received)
		if err != nil {
			t.Fatal(err)
		}
		invalidTask = task.Validate(time.Now().UTC()) != nil
	}
	if tasks != 1 || results != 0 || !expiresAt.After(time.Now()) || elapsed < 600*time.Millisecond {
		t.Fatalf("invalid dispatch snapshot after writer contention: tasks=%d results=%d remaining=%s elapsed=%s", tasks, results, time.Until(expiresAt), elapsed)
	}
	if dispatchErr != nil || invalidTask {
		t.Fatalf("dispatch lost its 100ms task window during SQLite writer wait: %v", dispatchErr)
	}
}
