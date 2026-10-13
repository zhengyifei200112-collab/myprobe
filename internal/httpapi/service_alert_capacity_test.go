package httpapi

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// This fixture tests the documented transfer limit, not real Agent throughput.
// All definitions are disabled and use synthetic targets/credentials.
func serviceAlertCapacityFixture(t *testing.T, db *store.Store) {
	t.Helper()
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Capacity observer"})
	if err != nil {
		t.Fatal(err)
	}
	config := store.ConfigSnapshot{Version: 2}
	channels := []store.NotificationChannel{}
	for i := 0; i < 1000; i++ {
		channel, err := db.CreateNotificationChannel(ctx, fmt.Sprintf("Capacity channel %04d", i), "webhook", "synthetic-capacity-credential")
		if err != nil {
			t.Fatal(err)
		}
		channels = append(channels, channel)
		config.HTTPServices = append(config.HTTPServices, store.ConfigHTTPService{ID: fmt.Sprintf("capacity-service-%04d", i), Name: fmt.Sprintf("Capacity service %04d", i), Enabled: false, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/capacity-private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	}
	if _, err := db.ImportConfig(ctx, config, false); err != nil {
		t.Fatal(err)
	}
	for i, v := range config.HTTPServices {
		if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: v.ID, ServiceRevision: 1, ChannelID: channels[i].ID, Enabled: false, CooldownSeconds: 300}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLiveServiceAlertCapacityBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_SERVICE_CAPACITY") != "1" {
		t.Skip("requires explicit capacity run and Edge/Playwright")
	}
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	serviceAlertCapacityFixture(t, db)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/service-alert-capacity-ui.cjs")
	cmd.Env = append(os.Environ(), "UI_BASE_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MYPROBE_CAPACITY_BASELINE") == "1" {
		return
	}
	// The UI exercised every row, and the lost-response replay must not apply
	// the batch a second time. Check persisted state beyond the visible page.
	for i := 0; i < 1000; i++ {
		v, err := db.ServiceAlertSettings(context.Background(), fmt.Sprintf("capacity-service-%04d", i))
		if err != nil {
			t.Fatal(err)
		}
		if v.Revision != 2 || v.ServiceRevision != 1 || v.CooldownSeconds != 301 || v.Enabled || v.CertificateRemindersEnabled {
			t.Fatalf("row %d: full batch or replay changed unexpectedly: %+v", i, v)
		}
	}
}
