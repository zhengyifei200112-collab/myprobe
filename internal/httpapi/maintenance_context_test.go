package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestMaintenanceContextAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	for _, path := range []string{"/api/v1/admin/incidents/missing/maintenance", "/api/v1/admin/notification-deliveries/missing/maintenance"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 401 || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("auth: %d %v", response.Code, response.Header())
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	read := func(path string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, path, "")
	}
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "context fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", "webhook", "unused")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := db.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Second)
	incident, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: at, Known: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	incidentPath := "/api/v1/admin/incidents/" + incident.ID + "/maintenance"
	deliveryPath := "/api/v1/admin/notification-deliveries/" + jobs[0].ID + "/maintenance"
	for _, path := range []string{incidentPath, deliveryPath} {
		r := read(path)
		if r.Code != 200 || !strings.Contains(r.Body.String(), `"windows":[]`) || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("empty: %d %s", r.Code, r.Body.String())
		}
	}
	w, err := db.CreateMaintenanceWindow(ctx, store.MaintenanceWindow{Reason: "window cause", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Second), EndsAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := db.ClaimDelivery(ctx, time.Now().UTC(), time.Minute); err != nil || claimed != nil {
		t.Fatalf("suppressed claim: %+v %v", claimed, err)
	}
	var incidentContext store.IncidentMaintenancePage
	response := read(incidentPath)
	if err := json.Unmarshal(response.Body.Bytes(), &incidentContext); err != nil || response.Code != 200 || !incidentContext.NodeInMaintenance || len(incidentContext.Windows) != 1 || incidentContext.Windows[0].ID != w.ID || len(incidentContext.Windows[0].Decisions) != 1 || incidentContext.Windows[0].Decisions[0].Kind != "delivery" {
		t.Fatalf("context: %s %v", response.Body.String(), err)
	}
	var deliveryContext store.DeliveryMaintenancePage
	response = read(deliveryPath)
	if err := json.Unmarshal(response.Body.Bytes(), &deliveryContext); err != nil || response.Code != 200 || len(deliveryContext.Windows) != 1 || deliveryContext.Windows[0].ID != w.ID || deliveryContext.Windows[0].RecordedAt.IsZero() {
		t.Fatalf("delivery: %s %v", response.Body.String(), err)
	}
	for _, path := range []string{incidentPath, deliveryPath} {
		for _, query := range []string{"?limit=", "?limit=0", "?limit=101", "?limit=x", "?limit=1&limit=2", "?after=a&after=b", "?after=" + strings.Repeat("x", 129)} {
			if r := read(path + query); r.Code != 400 {
				t.Fatalf("invalid query: %d %s", r.Code, r.Body.String())
			}
		}
	}
	for _, path := range []string{"/api/v1/admin/incidents/missing/maintenance", "/api/v1/admin/notification-deliveries/missing/maintenance"} {
		if r := read(path); r.Code != 404 {
			t.Fatalf("missing: %d", r.Code)
		}
	}
	if err := db.CancelMaintenanceWindow(ctx, w.ID, 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	response = read(incidentPath)
	if err := json.Unmarshal(response.Body.Bytes(), &incidentContext); err != nil || incidentContext.NodeInMaintenance || len(incidentContext.Windows) != 1 || incidentContext.Windows[0].Active {
		t.Fatalf("history: %s %v", response.Body.String(), err)
	}
}
