package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNetworkQualitySeriesAPIGridPopulationAndProjection(t *testing.T) {
	f := networkQualityAPIFixture(t)
	ids := []string{}
	for _, n := range f.nodes {
		ids = append(ids, n.ID)
	}
	url := "/api/v1/public/network-quality/series?" + f.query(ids...)
	r := f.get(url, nil)
	if r.Code != 200 {
		t.Fatal("series failed", r.Code)
	}
	var body struct {
		Aggregation string `json:"aggregation"`
		Bucket      int64  `json:"bucket_seconds"`
		Limit       int    `json:"point_limit"`
		Start, End  time.Time
		Nodes       []qualitySeriesNodeDTO `json:"nodes"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Aggregation != "time_series" || body.Bucket != 60 || body.Limit != 2000 || len(body.Nodes) != 8 {
		t.Fatal("invalid eight-node time-series envelope")
	}
	for i, n := range body.Nodes {
		if n.NodeID != ids[i] || len(n.Points) < 60 || len(n.Points) > 2000 {
			t.Fatal("node order or bounded grid changed")
		}
		var planned, pending, success, failure, missing uint64
		for j, p := range n.Points {
			if i > 0 && (!p.Time.Equal(body.Nodes[0].Points[j].Time) || !p.End.Equal(body.Nodes[0].Points[j].End)) {
				t.Fatal("observers received different time grids")
			}
			if p.Availability == "untracked_history" || p.Availability == "resolution_clipped" {
				if p.Counts.Planned != nil || p.RTTMean != nil || p.EffectiveStart != nil || p.EffectiveEnd != nil {
					t.Fatal("unavailable point became measured")
				}
				continue
			}
			if p.Counts.Planned == nil {
				t.Fatal("known point lacks counts")
			}
			planned += *p.Counts.Planned
			pending += *p.Counts.Pending
			success += *p.Counts.Success
			failure += *p.Counts.Failure
			missing += *p.Counts.Missing
			if *p.Counts.Success == 0 && p.RTTMean != nil {
				t.Fatal("missing point became zero RTT")
			}
		}
		if n.Counts.Planned == nil || planned != *n.Counts.Planned || pending != *n.Counts.Pending || success != *n.Counts.Success || failure != *n.Counts.Failure || missing != *n.Counts.Missing {
			t.Fatal("series does not partition node summary")
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	allowed := strings.Fields("time end availability effective_start effective_end counts coverage_pct execution_failure_pct rtt_mean_ms rtt_min_ms rtt_max_ms rtt_p50_ms rtt_p95_ms rtt_p50_bounds rtt_p95_bounds quantile_method address_family packet_loss")
	for _, node := range raw["nodes"].([]any) {
		for _, point := range node.(map[string]any)["points"].([]any) {
			fields := point.(map[string]any)
			if len(fields) != len(allowed) {
				t.Fatal("point exposes unexpected fields")
			}
			for _, name := range allowed {
				if _, ok := fields[name]; !ok {
					t.Fatal("point schema changed", name)
				}
			}
		}
	}
	for _, private := range append(f.taskIDs, "secret-network-target.example.test", "epoch_id", "result_sha", "lease_token", "\"host\"", "\"port\"") {
		if strings.Contains(r.Body.String(), private) {
			t.Fatal("private point data leaked")
		}
	}
	// A retained query uses coarse source-aligned buckets and explicit clipping,
	// while valid RTTs retain histogram bounds rather than precise raw labels.
	if _, err := f.db.RetainNetworkQuality(context.Background(), time.Now().UTC(), store.RetentionPolicy{Raw: time.Minute, OneMinute: 5 * time.Minute, FiveMinute: 2 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	r = f.get(url, nil)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || body.Bucket != 300 {
		t.Fatal("retained series resolution failed", r.Code)
	}
	histogram := false
	for _, p := range body.Nodes[0].Points {
		if p.QuantileMethod == "histogram_v1" {
			histogram = true
			if p.P50Bounds == nil || p.P50Bounds.LowerMS > 22 || p.P50Bounds.UpperMS < 22 {
				t.Fatal("series histogram excludes true median")
			}
		}
	}
	if !histogram {
		t.Fatal("retained precision disappeared")
	}
}

func TestNetworkQualitySeriesAPIAuthUnknownAndPointCap(t *testing.T) {
	f := networkQualityAPIFixture(t)
	path := "/api/v1/public/network-quality/series?"
	if r := f.get(path+f.query(f.nodes[0].ID, f.hidden.ID), nil); r.Code != 404 {
		t.Fatal("series bypassed public visibility", r.Code)
	}
	cookie, _ := loginAdminForShare(t, f.server.Handler())
	if r := f.get("/api/v1/admin/network-quality/series?"+f.query(f.hidden.ID), nil); r.Code != 401 {
		t.Fatal("anonymous admin series accepted")
	}
	if r := f.get("/api/v1/admin/network-quality/series?"+f.query(f.hidden.ID), cookie); r.Code != 200 {
		t.Fatal("authorized admin series rejected", r.Code)
	}
	r := f.get(path+strings.Replace(f.query(f.nodes[0].ID), "range=1h", "range=1y", 1), nil)
	var body struct {
		Bucket int64                  `json:"bucket_seconds"`
		Nodes  []qualitySeriesNodeDTO `json:"nodes"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || len(body.Nodes) != 1 || len(body.Nodes[0].Points) == 0 || len(body.Nodes[0].Points) > 2000 || body.Bucket < 300 {
		t.Fatal("year series is unbounded or missing")
	}
	if body.Nodes[0].Points[0].Availability != "untracked_history" || body.Nodes[0].Points[0].Counts.Planned != nil {
		t.Fatal("old year points became invented coverage")
	}
	// No client-supplied source/point/privilege flags bypass bounded Server choice.
	for _, suffix := range []string{"&bucket_seconds=1", "&point_limit=100000", "&include_hidden=1"} {
		if r = f.get(path+f.query(f.nodes[0].ID)+suffix, nil); r.Code != 400 {
			t.Fatal("client bypassed series limits", suffix, r.Code)
		}
	}
}
