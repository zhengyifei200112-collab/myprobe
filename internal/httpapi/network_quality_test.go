package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type qualityAPIFixture struct {
	server        *Server
	db            *store.Store
	nodes         []store.Node
	hidden, empty store.Node
	target        store.Target
	taskIDs       []string
}

func networkQualityAPIFixture(t *testing.T) qualityAPIFixture {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(networkQualityAPIFixtureDir(t), "quality-api.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := qualityAPIFixture{db: db}
	for range 8 {
		n, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic quality observer"})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, n)
	}
	f.hidden, _, err = db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic hidden observer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.UpdateNode(ctx, f.hidden.ID, store.UpdateNodeParams{Name: f.hidden.Name, Hidden: true, LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	f.empty, _, err = db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic unassigned observer"})
	if err != nil {
		t.Fatal(err)
	}
	f.target, err = db.CreateTarget(ctx, store.CreateTargetParams{Name: "Allowed comparison alias", Kind: "ping", Host: "secret-network-target.example.test", IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range append(append([]store.Node{}, f.nodes...), f.hidden) {
		if err = db.AssignTarget(ctx, n.ID, f.target.ID); err != nil {
			t.Fatal(err)
		}
	}
	inspection, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(path))+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	anchor := time.Now().UTC().Truncate(5 * time.Minute).Add(-10 * time.Minute)
	if _, err = inspection.Exec(`UPDATE network_schedule_epochs SET starts_at=?`, anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = inspection.Exec(`UPDATE network_schedule_metadata SET known_since=?,known_results_since=?`, anchor.Unix(), anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	assignments, err := db.ListNetworkAssignments(ctx, anchor)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assignments {
		for i, class := range []string{"", "timeout", "busy", "offline", "", ""} {
			slot := anchor.Add(time.Duration(i) * 30 * time.Second)
			task, err := db.ClaimNetworkTask(ctx, a.EpochID, slot, slot.Add(time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			f.taskIDs = append(f.taskIDs, task.Task.ID)
			if class == "offline" {
				if err = db.FinishNetworkDispatch(ctx, task, "offline", slot); err != nil {
					t.Fatal(err)
				}
				continue
			}
			rtt := float64(10 + i*3)
			if class != "" {
				rtt = 0
			}
			if _, err = db.AcceptNetworkResult(ctx, a.NodeID, "ping", protocol.LatencyResult{TaskID: task.Task.ID, TargetID: f.target.ID, Success: class == "", LatencyMS: rtt, ErrorClass: class, CompletedAt: slot.Add(time.Second)}, slot.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
		}
	}
	service := auth.New(db, time.Hour)
	if _, err = service.Bootstrap(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	f.server = New(config.Config{}, db, service, agentgateway.New(db, hub), hub)
	return f
}

func (f qualityAPIFixture) query(ids ...string) string {
	v := url.Values{"target_id": {f.target.ID}, "kind": {"ping"}, "range": {"1h"}, "node_id": ids}
	return v.Encode()
}
func (f qualityAPIFixture) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	r := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(r, request)
	return r
}

func TestNetworkQualityAPIProjectionAndEightNodeLimit(t *testing.T) {
	f := networkQualityAPIFixture(t)
	ids := []string{}
	for _, n := range f.nodes {
		ids = append(ids, n.ID)
	}
	r := f.get("/api/v1/public/network-quality?"+f.query(ids...), nil)
	if r.Code != 200 || r.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("quality batch failed or can be cached", r.Code, r.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	exactKeys := func(value any, allowed string) {
		t.Helper()
		m, ok := value.(map[string]any)
		if !ok {
			t.Fatal("projection is not an object")
		}
		keys := strings.Fields(allowed)
		if len(m) != len(keys) {
			t.Fatalf("projection keys differ: %v", m)
		}
		for _, key := range keys {
			if _, ok := m[key]; !ok {
				t.Fatal("missing projection key", key)
			}
		}
	}
	exactKeys(body, "version aggregation direction target address_family packet_loss requested_start requested_end as_of timezone nodes")
	exactKeys(body["target"], "id name kind")
	exactKeys(body["address_family"], "status reason")
	exactKeys(body["packet_loss"], "value_pct status reason")
	nodes := body["nodes"].([]any)
	if len(nodes) != 8 {
		t.Fatal("batch did not return eight nodes")
	}
	for _, node := range nodes {
		exactKeys(node, "node_id name availability available_from effective_start effective_end granularity_seconds counts coverage_pct execution_failure_pct rtt_mean_ms rtt_min_ms rtt_max_ms rtt_p50_ms rtt_p95_ms rtt_p50_bounds rtt_p95_bounds quantile_method jitter_ms jitter_pairs failure_causes not_executed_causes unobserved_task_causes")
		n := node.(map[string]any)
		exactKeys(n["counts"], "planned pending success failure missing not_executed unobserved_tasks")
		c := n["counts"].(map[string]any)
		if c["success"] != float64(3) || c["failure"] != float64(1) || c["not_executed"] != float64(1) || c["unobserved_tasks"] != float64(1) || n["execution_failure_pct"] != float64(25) || n["rtt_p50_ms"] != float64(22) || n["rtt_p95_ms"] != float64(25) || n["jitter_ms"] != float64(3) || n["jitter_pairs"] != float64(1) {
			t.Fatal("projected manual population differs", n)
		}
		exactKeys(n["failure_causes"], "timeout dns refused unreachable")
		exactKeys(n["not_executed_causes"], "busy unsupported invalid_task")
		exactKeys(n["unobserved_task_causes"], "offline write_error dispatch_unknown missing")
	}
	if body["address_family"].(map[string]any)["status"] != "unknown" || body["packet_loss"].(map[string]any)["value_pct"] != nil {
		t.Fatal("protocol evidence was invented")
	}
	for _, private := range append(f.taskIDs, "secret-network-target.example.test", "result_sha", "lease_token", "epoch_id", "\"host\"", "\"port\"") {
		if strings.Contains(r.Body.String(), private) {
			t.Fatal("private evidence leaked", private)
		}
	}
	if r = f.get("/api/v1/public/network-quality?"+f.query(append(ids, f.empty.ID)...), nil); r.Code != 400 {
		t.Fatal("ninth node accepted", r.Code)
	}
	if r = f.get("/api/v1/public/network-quality?"+f.query(f.nodes[0].ID, f.nodes[0].ID), nil); r.Code != 400 {
		t.Fatal("duplicate node accepted", r.Code)
	}
	r = f.get("/api/v1/public/network-quality?"+f.query(f.nodes[0].ID, f.empty.ID), nil)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	empty := body["nodes"].([]any)[1].(map[string]any)
	if empty["availability"] != "no_plan_history" || empty["counts"].(map[string]any)["planned"] != nil || empty["coverage_pct"] != nil {
		t.Fatal("absent history became perfect coverage")
	}
	if _, err := f.db.RetainNetworkQuality(context.Background(), time.Now().UTC(), store.RetentionPolicy{Raw: time.Minute, OneMinute: 5 * time.Minute, FiveMinute: 2 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	r = f.get("/api/v1/public/network-quality?"+f.query(f.nodes[0].ID), nil)
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil {
		t.Fatal("rolled quality projection failed", r.Code, r.Body.String())
	}
	rolled := body["nodes"].([]any)[0].(map[string]any)
	if rolled["quantile_method"] != "histogram_v1" {
		t.Fatal("rollup precision was hidden", rolled)
	}
	for _, name := range []string{"rtt_p50_bounds", "rtt_p95_bounds"} {
		exactKeys(rolled[name], "lower_ms upper_ms")
	}
	bound := rolled["rtt_p50_bounds"].(map[string]any)
	if bound["lower_ms"].(float64) > 22 || bound["upper_ms"].(float64) < 22 {
		t.Fatal("projected histogram excludes true median")
	}
}

func TestNetworkQualityAPIAuthAndShareIsolation(t *testing.T) {
	f := networkQualityAPIFixture(t)
	ctx := context.Background()
	query := f.query(f.nodes[0].ID, f.hidden.ID)
	r := f.get("/api/v1/public/network-quality?"+query, nil)
	if r.Code != 404 || strings.Contains(r.Body.String(), f.target.Name) {
		t.Fatal("mixed public batch leaked authorized part", r.Code, r.Body.String())
	}
	cookie, _ := loginAdminForShare(t, f.server.Handler())
	if r = f.get("/api/v1/admin/network-quality?"+query, nil); r.Code != 401 {
		t.Fatal("anonymous admin accepted", r.Code)
	}
	if r = f.get("/api/v1/admin/network-quality?"+query, cookie); r.Code != 200 {
		t.Fatal("admin hidden comparison rejected", r.Code, r.Body.String())
	}
	share, err := f.server.sharing.Create(ctx, "Synthetic comparison share", "synthetic-share-password", []string{f.nodes[0].ID, f.hidden.ID})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/share/" + share.ID
	if r = f.get(base+"/network-quality?"+query, cookie); r.Code != 401 {
		t.Fatal("admin cookie substituted for share session", r.Code)
	}
	loginShare := func() *http.Cookie {
		login := httptest.NewRequest(http.MethodPost, base+"/login", bytes.NewBufferString(`{"password":"synthetic-share-password"}`))
		login.Header.Set("Content-Type", "application/json")
		reply := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(reply, login)
		if reply.Code != 200 || len(reply.Result().Cookies()) != 1 {
			t.Fatal("share login failed", reply.Code)
		}
		return reply.Result().Cookies()[0]
	}
	shareCookie := loginShare()
	if r = f.get(base+"/network-quality?"+query, shareCookie); r.Code != 200 {
		t.Fatal("explicit private share rejected", r.Code, r.Body.String())
	}
	if r = f.get("/api/v1/admin/network-quality?"+query, shareCookie); r.Code != 401 {
		t.Fatal("share token elevated to admin")
	}
	if r = f.get(base+"/network-quality?"+f.query(f.nodes[0].ID, f.nodes[1].ID), shareCookie); r.Code != 404 || strings.Contains(r.Body.String(), f.target.Name) {
		t.Fatal("share crossed its selected nodes", r.Code)
	}
	if _, err = f.db.UpdateChartShare(ctx, share.ID, share.Name, nil, []string{f.nodes[0].ID}, true); err != nil {
		t.Fatal(err)
	}
	if r = f.get(base+"/network-quality?"+query, shareCookie); r.Code != 401 {
		t.Fatal("scope edit did not revoke prior share session", r.Code)
	}
	shareCookie = loginShare()
	if r = f.get(base+"/network-quality?"+query, shareCookie); r.Code != 404 {
		t.Fatal("new session accessed removed share node", r.Code)
	}
	if r = f.get(base+"/network-quality?"+f.query(f.nodes[0].ID), shareCookie); r.Code != 200 {
		t.Fatal("new session lost retained share node", r.Code)
	}
	if _, err = f.db.UpdateChartShare(ctx, share.ID, share.Name, nil, []string{f.nodes[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	if r = f.get(base+"/network-quality?"+f.query(f.nodes[0].ID), shareCookie); r.Code != 401 {
		t.Fatal("disabled share session accepted", r.Code)
	}
}

func TestNetworkQualityAPIRejectsControlCharacters(t *testing.T) {
	f := networkQualityAPIFixture(t)
	for _, key := range []string{"node_id", "target_id"} {
		for _, control := range []string{"\x01", "\x7f", "\u0085"} {
			q, err := url.ParseQuery(f.query(f.nodes[0].ID))
			if err != nil {
				t.Fatal(err)
			}
			q.Set(key, "synthetic"+control+"id")
			r := f.get("/api/v1/public/network-quality?"+q.Encode(), nil)
			if r.Code != http.StatusBadRequest || r.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("control character in %s treated as a lookup: %d", key, r.Code)
			}
		}
	}
}

func TestNetworkQualityQueryBoundariesAndTimeZones(t *testing.T) {
	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	base := url.Values{"node_id": {"synthetic-node"}, "target_id": {"synthetic-target"}, "kind": {"ping"}}
	for _, bad := range []string{"&range=2y", "&timezone=Local", "&timezone=Missing/Zone", "&include_hidden=1", "&target_id=duplicate", "&start=2026-01-01T00:00:00Z", "&end=2026-01-01T00:00:00Z", "&start=2026-01-01T00:00:00Z&end=2026-04-02T00:00:00Z", "&start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z&range=1d", "&broken=%ZZ"} {
		if _, _, err := parseNetworkQualityQuery(base.Encode()+bad, now); err == nil {
			t.Fatal("invalid query accepted", bad)
		}
	}
	v := base
	v.Set("start", "2026-03-08T01:30:00-05:00")
	v.Set("end", "2026-03-08T03:30:00-04:00")
	v.Set("timezone", "America/New_York")
	q, tz, err := parseNetworkQualityQuery(v.Encode(), now)
	if err != nil || q.End.Sub(q.Start) != time.Hour || tz != "America/New_York" {
		t.Fatal("DST absolute interval changed", q, tz, err)
	}
	v.Set("start", "2026-03-08T23:30:00+08:00")
	v.Set("end", "2026-03-09T00:30:00+08:00")
	v.Set("timezone", "Asia/Shanghai")
	q, tz, err = parseNetworkQualityQuery(v.Encode(), now)
	if err != nil || q.End.Sub(q.Start) != time.Hour || tz != "Asia/Shanghai" {
		t.Fatal("cross-day absolute range changed", q, tz, err)
	}
}

func TestNetworkQualityAPIUnavailableHistoryAndCorruption(t *testing.T) {
	f := networkQualityAPIFixture(t)
	v := url.Values{"node_id": {f.nodes[0].ID}, "target_id": {f.target.ID}, "kind": {"ping"}, "start": {time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)}, "end": {time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)}}
	r := f.get("/api/v1/public/network-quality?"+v.Encode(), nil)
	var body struct {
		Nodes []qualityNodeDTO `json:"nodes"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || len(body.Nodes) != 1 || body.Nodes[0].Availability != "untracked_history" || body.Nodes[0].Counts.Planned != nil || body.Nodes[0].JitterPairs != nil || body.Nodes[0].Coverage != nil {
		t.Fatal("unknown history became zero or healthy", r.Code, r.Body.String())
	}
	inspection, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(f.db.DatabasePath()))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	if _, err = inspection.Exec(`UPDATE network_probe_tasks SET result_json='{}' WHERE source='scheduled' AND state='completed'`); err != nil {
		t.Fatal(err)
	}
	r = f.get("/api/v1/public/network-quality?"+f.query(f.nodes[0].ID, f.nodes[1].ID), nil)
	if r.Code != 503 || strings.Contains(r.Body.String(), f.target.Name) || strings.Contains(r.Body.String(), f.nodes[0].Name) {
		t.Fatal("corrupt batch exposed partial results", r.Code, r.Body.String())
	}
}
