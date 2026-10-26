package httpapi

import (
	"context"
	"encoding/json"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestLiveIncidentSilenceBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_SILENCE_BROWSER") != "1" {
		t.Skip("requires Playwright/Edge; opt in with MYPROBE_TEST_SILENCE_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	f := newSilenceRuntime(t)
	other, _, err := f.db.CreateNode(ctx, store.CreateNodeParams{Name: "Other silence node"})
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := f.db.CreateNotificationChannel(ctx, "disabled fixture", "webhook", "unused")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.UpdateNotificationChannel(ctx, disabled.ID, disabled.Name, disabled.Kind, nil, false); err != nil {
		t.Fatal(err)
	}
	rule, err := f.db.CreateAlertRule(ctx, other.ID, disabled.ID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ObserveAlert(ctx, rule, other, store.AlertObservation{At: time.Now().UTC(), Known: true, Active: true}); err != nil {
		t.Fatal(err)
	}
	actor, err := f.db.UserByUsername(ctx, "browser-admin")
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.db.CreateMaintenanceWindow(ctx, store.MaintenanceWindow{Reason: "Browser maintenance", Timezone: "UTC", StartsAt: time.Now().UTC().Add(-time.Minute), EndsAt: time.Now().UTC().Add(time.Hour), NodeIDs: []string{f.node.ID}, CreatedBy: actor.ID})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.handler)
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		sent := false
		for ctx.Err() == nil {
			page, err := f.db.ListIncidentSilences(ctx, f.incident.ID, "", 25, time.Now().UTC())
			if err != nil {
				if ctx.Err() == nil {
					t.Error(err)
				}
				return
			}
			maintenance, err := f.db.ListIncidentMaintenance(ctx, f.incident.ID, "", 25, time.Now().UTC())
			if err != nil {
				if ctx.Err() == nil {
					t.Error(err)
				}
				return
			}
			if len(page.Silences) > 0 && page.ActiveSilence == nil && !maintenance.NodeInMaintenance {
				shouldTick := !sent
				if sent {
					nodes, err := f.db.ListNodes(ctx)
					if err != nil {
						return
					}
					for _, node := range nodes {
						if node.ID == f.node.ID && node.ExpiresAt != nil && node.ExpiresAt.After(time.Now().UTC().Add(48*time.Hour)) {
							shouldTick = true
						}
					}
				}
				if shouldTick {
					if err := f.svc.Tick(ctx, time.Now().UTC()); err != nil {
						t.Error(err)
						return
					}
					if worked, err := f.svc.DeliverOne(ctx, time.Now().UTC()); err != nil || !worked {
						t.Errorf("real delivery: %v %v", worked, err)
						return
					}
					if sent {
						return
					}
					sent = true
				}
			}
			select {
			case <-time.After(25 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	command := exec.CommandContext(ctx, "node", "../../scripts/incident-silence-live-ui.cjs")
	command.Env = append(os.Environ(), "SILENCE_BROWSER_URL="+server.URL, "SILENCE_BROWSER_PASSWORD="+f.password, "SILENCE_BROWSER_MAINTENANCE="+w.ID)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"firing", "resolved"} {
		select {
		case got := <-f.receipts:
			if got.IncidentID != f.incident.ID || got.State != state {
				t.Fatalf("receipt: %+v", got)
			}
		default:
			t.Fatal("missing receipt")
		}
	}
	if len(f.receipts) != 0 {
		t.Fatal("extra browser receipts")
	}
}
