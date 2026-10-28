package httpapi

import (
	"context"
	"database/sql"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNetworkQualityLiveComparisonBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NETWORK_QUALITY_BROWSER") != "1" {
		t.Skip("opt-in real backend comparison browser")
	}
	f := networkQualityAPIFixture(t)
	ctx := context.Background()
	for i, n := range f.nodes {
		if _, err := f.db.UpdateNode(ctx, n.ID, store.UpdateNodeParams{Name: fmt.Sprintf("观测节点 %02d", i+1), LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.UpdateNode(ctx, f.hidden.ID, store.UpdateNodeParams{Name: "隐藏观测节点", Hidden: true, LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.UpdateNode(ctx, f.empty.ID, store.UpdateNodeParams{Name: "空白观测节点", LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	port := 443
	targets := []store.Target{f.target}
	for _, definition := range []store.CreateTargetParams{{Name: "IPv4 配置目标", Kind: "tcping", Host: "192.0.2.53", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000}, {Name: "IPv6 配置目标", Kind: "tcping", Host: "2001:db8::53", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000}} {
		target, err := f.db.CreateTarget(ctx, definition)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
		for _, n := range append(append([]store.Node{}, f.nodes...), f.hidden) {
			if err = f.db.AssignTarget(ctx, n.ID, target.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	inspection, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(f.db.DatabasePath()))+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	anchor := time.Now().UTC().Truncate(5 * time.Minute).Add(-2 * time.Hour)
	if _, err = inspection.Exec(`UPDATE network_schedule_epochs SET starts_at=?`, anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = inspection.Exec(`UPDATE network_schedule_metadata SET known_since=?,known_results_since=?`, anchor.Unix(), anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	assignments, err := f.db.ListNetworkAssignments(ctx, anchor)
	if err != nil {
		t.Fatal(err)
	}
	for j, a := range assignments {
		kind := "ping"
		for _, target := range targets {
			if target.ID == a.TargetID {
				kind = target.Kind
			}
		}
		for i := 0; i < 100; i++ {
			if i%17 == 3 {
				continue
			}
			slot := anchor.Add(time.Duration(i) * 30 * time.Second)
			task, err := f.db.ClaimNetworkTask(ctx, a.EpochID, slot, slot.Add(time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if i%13 == 2 {
				if err = f.db.FinishNetworkDispatch(ctx, task, "offline", slot); err != nil {
					t.Fatal(err)
				}
				continue
			}
			class := ""
			if i%13 == 0 {
				class = "timeout"
			}
			if i%13 == 1 {
				class = "unsupported"
			}
			if kind == "tcping" && a.NodeID == f.nodes[0].ID {
				for _, target := range targets {
					if target.ID == a.TargetID && target.Host == "2001:db8::53" {
						class = "unsupported"
					}
				}
			}
			rtt := float64(10+j*2) + float64(i%20)/2
			if class != "" {
				rtt = 0
			}
			if _, err = f.db.AcceptNetworkResult(ctx, a.NodeID, kind, protocol.LatencyResult{TaskID: task.Task.ID, TargetID: a.TargetID, Success: class == "", LatencyMS: rtt, ErrorClass: class, CompletedAt: slot.Add(time.Second)}, slot.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	share, err := f.server.sharing.Create(ctx, "演示私有网络质量", "synthetic-browser-share", []string{f.nodes[0].ID, f.hidden.ID})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(f.server.Handler())
	defer server.Close()
	runCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "node", "../../scripts/network-quality-browser.cjs")
	ids := ""
	for i, n := range f.nodes {
		if i > 0 {
			ids += ","
		}
		ids += n.ID
	}
	cmd.Env = append(os.Environ(), "UI_BASE_URL="+server.URL, "QA_PUBLIC_IDS="+ids, "QA_HIDDEN_ID="+f.hidden.ID, "QA_EMPTY_ID="+f.empty.ID, "QA_SHARE_ID="+share.ID, "QA_SHARE_PASSWORD=synthetic-browser-share")
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
