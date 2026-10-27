package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLiveHTTPStatisticsControlledBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") != "1" {
		t.Skip("requires local Edge and Playwright")
	}
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/http-services-ui.cjs")
	cmd.Env = append(os.Environ(), "UI_BASE_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
