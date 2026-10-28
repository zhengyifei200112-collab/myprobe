package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func seedNetworkQualityAPIEvidence(t *testing.T, f qualityAPIFixture) {
	t.Helper()
	ctx := context.Background()
	raw, err := sql.Open("sqlite", f.db.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, node := range []store.Node{f.nodes[0], f.hidden} {
		var epoch string
		var start int64
		if err = raw.QueryRow(`SELECT id,starts_at FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, node.ID, f.target.ID).Scan(&epoch, &start); err != nil {
			t.Fatal(err)
		}
		connection := strings.Repeat("e", 32)
		if _, err = f.db.BeginAgentConnection(ctx, node.ID, connection, 2, false, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			slot := time.Unix(start, 0).UTC().Add(time.Duration(6+i) * 30 * time.Second)
			task, err := f.db.ClaimNetworkTask(ctx, epoch, slot, slot.Add(time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			if err = f.db.RequireNetworkEvidence(ctx, task, connection); err != nil {
				t.Fatal(err)
			}
			rtt := 15.0
			r := check.Result{TaskID: task.Task.ID, TargetID: f.target.ID, Kind: "ping", Outcome: check.Success, AddressFamily: check.FamilyIPv4, RTTMS: &rtt, Packets: &check.PacketCounts{Sent: 1, Received: 1}, CompletedAt: slot.Add(time.Second)}
			if i == 1 {
				r.Outcome = check.Failure
				r.AddressFamily = check.FamilyIPv6
				r.RTTMS = nil
				r.Packets.Received = 0
				r.ErrorClass = "timeout"
			}
			if ok, err := f.db.AcceptNetworkProbeResult(ctx, node.ID, connection, r, slot.Add(time.Second)); err != nil || !ok {
				t.Fatal(err)
			}
		}
	}
}

func TestNetworkQualityEvidencePublicAdminShareProjection(t *testing.T) {
	f := networkQualityAPIFixture(t)
	seedNetworkQualityAPIEvidence(t, f)
	ctx := context.Background()
	admin, _ := loginAdminForShare(t, f.server.Handler())
	share, err := f.server.sharing.Create(ctx, "Synthetic evidence share", "synthetic-evidence-password", []string{f.nodes[0].ID, f.hidden.ID})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/share/" + share.ID
	login := httptest.NewRequest(http.MethodPost, base+"/login", bytes.NewBufferString(`{"password":"synthetic-evidence-password"}`))
	login.Header.Set("Content-Type", "application/json")
	reply := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(reply, login)
	if reply.Code != 200 {
		t.Fatal("share login", reply.Code)
	}
	shareCookie := reply.Result().Cookies()[0]
	for _, suffix := range []string{"", "/series"} {
		for _, tc := range []struct {
			path   string
			cookie *http.Cookie
			ids    []string
		}{{"/api/v1/public", nil, []string{f.nodes[0].ID}}, {"/api/v1/admin", admin, []string{f.hidden.ID}}, {base, shareCookie, []string{f.hidden.ID}}} {
			r := f.get(tc.path+"/network-quality"+suffix+"?"+f.query(tc.ids...), tc.cookie)
			var body struct {
				Nodes []qualitySeriesNodeDTO `json:"nodes"`
			}
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || len(body.Nodes) != 1 {
				t.Fatal("evidence projection unavailable", r.Code, r.Body.String())
			}
			n := body.Nodes[0]
			if n.AddressFamily.Status != "partial" || n.AddressFamily.IPv4 == nil || *n.AddressFamily.IPv4 != 1 || *n.AddressFamily.IPv6 != 1 || *n.AddressFamily.Unknown != 4 {
				t.Fatal("family coverage invented", n.AddressFamily)
			}
			if n.PacketLoss.Status != "partial" || n.PacketLoss.Value == nil || *n.PacketLoss.Value != 50 || *n.PacketLoss.Measured != 2 || *n.PacketLoss.Unknown != 4 || *n.PacketLoss.Sent != 2 || *n.PacketLoss.Received != 1 {
				t.Fatal("weighted loss/coverage differs", n.PacketLoss)
			}
			if suffix == "/series" {
				var sent, received uint64
				for _, p := range n.Points {
					if p.PacketLoss.Sent != nil {
						sent += *p.PacketLoss.Sent
						received += *p.PacketLoss.Received
					}
				}
				if sent != 2 || received != 1 {
					t.Fatal("point evidence does not partition range")
				}
			}
			for _, private := range []string{f.target.Host, "result_json", "result_sha", "lease_token", "epoch_id", "\"host\"", "\"port\"", "completed_at", "tool_output"} {
				if strings.Contains(r.Body.String(), private) {
					t.Fatal("evidence leaked private data", private)
				}
			}
		}
	}
	if r := f.get("/api/v1/public/network-quality?"+f.query(f.hidden.ID), nil); r.Code != 404 {
		t.Fatal("hidden evidence exposed", r.Code)
	}
}

func TestNetworkQualityEvidenceProjectionBoundaries(t *testing.T) {
	legacy := store.NetworkQualityStats{Availability: "available", Success: 2}
	lf, lp := projectQualityEvidence(legacy, "ping")
	if lf.Status != "unknown" || lp.Status != "unavailable" || lp.Sent != nil || lp.Received != nil || lp.Value != nil || lp.Unknown == nil || *lp.Unknown != 2 {
		t.Fatal("legacy packets fabricated as zero", lf, lp)
	}
	s := store.NetworkQualityStats{Availability: "available", Success: 2, Failure: 1, Totals: quality.Totals{Evidence: quality.EvidenceTotals{IPv4: 2, IPv6: 1, PacketSuccess: 2, PacketFailure: 1, Sent: 7, Received: 4}}}
	f, p := projectQualityEvidence(s, "ping")
	if f.Status != "mixed" || p.Status != "available" || p.Value == nil || *p.Value != 100*3.0/7 {
		t.Fatal("not weighted by packets", f, p)
	}
	_, p = projectQualityEvidence(s, "tcping")
	if p.Status != "not_applicable" || p.Value != nil || p.Sent != nil {
		t.Fatal("TCP claimed ICMP packet loss", p)
	}
	for _, availability := range []string{"no_plan_history", "untracked_history", "resolution_clipped"} {
		s.Availability = availability
		f, p = projectQualityEvidence(s, "ping")
		if f.IPv4 != nil || p.Value != nil || p.Sent != nil {
			t.Fatal("unknown range became zero", availability)
		}
	}
}
