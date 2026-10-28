package agentclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func TestInvalidManagedWelcomeDoesNotPartiallyApplyLegacyCopy(t *testing.T) {
	for _, configuration := range []*v2.RuntimeConfiguration{nil, {Scope: v2.SamplingConfigurationScope, Revision: 1, Nonce: "invalid", CollectionSeconds: 2, ReportSeconds: 3}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			connection, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer connection.CloseNow()
			var hello v1.Envelope
			if wsjson.Read(ctx, connection, &hello) != nil {
				return
			}
			welcome, _ := v2.NewEnvelope(v1.TypeWelcome, 1, v2.Welcome{Welcome: v1.Welcome{Config: v1.Config{CollectionSeconds: 8, ReportSeconds: 9}}, Capabilities: []string{v2.ConfigurationCapability}, RuntimeConfig: configuration})
			_ = wsjson.Write(ctx, connection, welcome)
			<-connection.CloseRead(ctx).Done()
		}))
		client := newSamplingFixtureClient(t, server.URL, time.Second, 2*time.Second)
		connected, err := client.connectAndRead(ctx)
		if err == nil || connected || client.currentConnection() != nil || client.collectionNS.Load() != int64(time.Second) || client.reportNS.Load() != int64(2*time.Second) {
			t.Error("invalid managed welcome changed local settings or published a connection")
		}
		cancel()
		server.Close()
	}
}
