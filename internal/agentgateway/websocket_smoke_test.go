package agentgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestWebSocketJSONRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		var request map[string]string
		if err := wsjson.Read(r.Context(), connection, &request); err != nil {
			t.Error(err)
			return
		}
		_ = wsjson.Write(r.Context(), connection, map[string]string{"reply": request["hello"]})
	}))
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	connection, _, err := websocket.Dial(context.Background(), endpoint, &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err := wsjson.Write(context.Background(), connection, map[string]string{"hello": "world"}); err != nil {
		t.Fatal(err)
	}
	var response map[string]string
	if err := wsjson.Read(context.Background(), connection, &response); err != nil {
		t.Fatal(err)
	}
	if response["reply"] != "world" {
		t.Fatalf("reply = %q", response["reply"])
	}
}
