package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestNetworkQualityTargetDiscoveryProjectionAndScope(t *testing.T) {
	f := networkQualityAPIFixture(t)
	ctx := context.Background()
	query := "node_id=" + f.nodes[0].ID
	r := f.get("/api/v1/public/network-quality/targets?"+query, nil)
	var body map[string]any
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || r.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("target discovery failed or cacheable", r.Code)
	}
	if len(body) != 4 || body["page_size"] != float64(128) {
		t.Fatal("target envelope changed")
	}
	list := body["targets"].([]any)
	if len(list) != 1 {
		t.Fatal("target discovery escaped selected observers")
	}
	alias := list[0].(map[string]any)
	if len(alias) != 3 || alias["id"] != f.target.ID || alias["name"] != f.target.Name || alias["kind"] != "ping" {
		t.Fatal("target projection leaked internals")
	}
	if strings.Contains(r.Body.String(), "secret-network-target.example.test") || strings.Contains(r.Body.String(), "\"port\"") {
		t.Fatal("target address leaked")
	}
	if r = f.get("/api/v1/public/network-quality/targets?"+query+"&node_id="+f.hidden.ID, nil); r.Code != 404 {
		t.Fatal("public discovery bypassed hidden scope")
	}
	cookie, _ := loginAdminForShare(t, f.server.Handler())
	if r = f.get("/api/v1/admin/network-quality/targets?node_id="+f.hidden.ID, cookie); r.Code != 200 {
		t.Fatal("admin discovery rejected", r.Code)
	}
	share, err := f.server.sharing.Create(ctx, "Synthetic discovery scope", "synthetic-discovery-password", []string{f.hidden.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := f.server.sharing.Login(ctx, share.ID, "synthetic-discovery-password", "192.0.2.10", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	shareCookie := &http.Cookie{Name: shareCookieName(share.ID), Value: token}
	path := "/api/v1/share/" + share.ID + "/network-quality/targets?node_id="
	if r = f.get(path+f.hidden.ID, shareCookie); r.Code != 200 {
		t.Fatal("authorized private discovery rejected", r.Code)
	}
	if r = f.get(path+f.nodes[0].ID, shareCookie); r.Code != 404 {
		t.Fatal("private discovery crossed membership", r.Code)
	}
	if r = f.get(path+f.hidden.ID, cookie); r.Code != 401 {
		t.Fatal("admin substituted for share session")
	}
	if _, err = f.db.UpdateChartShare(ctx, share.ID, share.Name, nil, share.NodeIDs, false); err != nil {
		t.Fatal(err)
	}
	if r = f.get(path+f.hidden.ID, shareCookie); r.Code != 401 {
		t.Fatal("disabled discovery share remained available")
	}
	for _, suffix := range []string{"&limit=100000", "&host=one", "&include_hidden=1", "&cursor=a&cursor=b", "&node_id=" + f.nodes[0].ID} {
		if r = f.get("/api/v1/public/network-quality/targets?"+query+suffix, nil); r.Code != 400 {
			t.Fatal("discovery boundary accepted", suffix, r.Code)
		}
	}
}
