package agentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestPreferredTransportFallbackBoundaries(t *testing.T) {
	for _, test := range []struct {
		name            string
		status, version int
		fallback        bool
	}{
		{"v2", 101, 2, false}, {"old_server", 404, 1, true}, {"method_unavailable", 405, 1, true},
		{"unauthorized", 401, 2, false}, {"forbidden", 403, 2, false}, {"server_error", 500, 2, false},
		{"unexpected_html", 200, 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var paths []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				paths = append(paths, r.URL.Path)
				mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing authentication")
				}
				if r.URL.Path == "/prefix/api/v2/agent/ws" && test.status != 101 {
					w.WriteHeader(test.status)
					return
				}
				connection, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer connection.CloseNow()
				<-connection.CloseRead(r.Context()).Done()
			}))
			defer server.Close()
			base, _ := url.Parse(server.URL + "/prefix")
			client := &Client{baseURL: base, config: Config{Token: "fixture-token"}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			connection, _, version, err := client.dialPreferred(ctx)
			wantSuccess := test.status == 101 || test.fallback
			if (err == nil) != wantSuccess || version != test.version {
				t.Fatalf("version=%d err=%v", version, err)
			}
			if connection != nil {
				connection.CloseNow()
			}
			want := []string{"/prefix/api/v2/agent/ws"}
			if test.fallback {
				want = append(want, "/prefix/api/v1/agent/ws")
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(paths, want) {
				t.Fatalf("paths=%v want=%v", paths, want)
			}
		})
	}
}
