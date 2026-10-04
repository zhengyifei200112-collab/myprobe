package agentclient

import (
	"context"
	"net/http"
	"path"
	"time"

	"github.com/coder/websocket"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func (c *Client) dialPreferred(ctx context.Context) (*websocket.Conn, *http.Response, int, error) {
	for _, version := range []int{2, 1} {
		u := *c.baseURL
		if u.Scheme == "https" {
			u.Scheme = "wss"
		} else {
			u.Scheme = "ws"
		}
		endpoint := "/api/v2/agent/ws"
		if version == 1 {
			endpoint = "/api/v1/agent/ws"
		}
		u.Path = path.Join(u.Path, endpoint)
		dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		connection, response, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + c.config.Token}}, CompressionMode: websocket.CompressionDisabled})
		cancel()
		if err == nil {
			return connection, response, version, nil
		}
		if version == 2 && response != nil && (response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed) {
			continue
		}
		return nil, response, version, err
	}
	panic("unreachable transport negotiation")
}

func validateServerEnvelope(e v1.Envelope, version int) error {
	if version == 2 {
		return v2.Envelope(e).Validate(time.Now().UTC())
	}
	return e.Validate(time.Now().UTC())
}
