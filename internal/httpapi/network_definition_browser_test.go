package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestNetworkDefinitionLiveBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NETWORK_QUALITY_BROWSER") != "1" {
		t.Skip("opt-in real requested-profile browser")
	}
	f := networkQualityAPIFixture(t)
	server := httptest.NewServer(f.server.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "../../scripts/network-definition-browser.cjs")
	command.Env = append(os.Environ(), "UI_BASE_URL="+server.URL, "QA_PROFILE_NODE_ID="+f.nodes[0].ID)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
