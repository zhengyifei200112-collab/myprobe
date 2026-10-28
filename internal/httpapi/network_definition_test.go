package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNetworkDefinitionAdminContractAndLegacyEdit(t *testing.T) {
	f := networkQualityAPIFixture(t)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	answer := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(answer, login)
	if answer.Code != http.StatusOK {
		t.Fatal("login failed")
	}
	var auth struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(answer.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	cookie := answer.Result().Cookies()[0]
	create := `{"name":"Synthetic requested profile","kind":"ping","host":"example.test","interval_seconds":60,"timeout_ms":3000,"address_family":"ipv6","packet_count":3}`
	r := authenticatedRequest(t, f.server.Handler(), cookie, auth.CSRF, http.MethodPost, "/api/v1/admin/targets", create)
	if r.Code != http.StatusCreated {
		t.Fatalf("profile create: %d %s", r.Code, r.Body.String())
	}
	var body struct {
		Target store.Target `json:"target"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Target.AddressFamily != "ipv6" || body.Target.PacketCount != 3 {
		t.Fatal("request options lost")
	}
	legacy := `{"name":"Renamed with legacy client","kind":"ping","host":"example.test","interval_seconds":60,"timeout_ms":3000,"enabled":true,"sort_order":0}`
	r = authenticatedRequest(t, f.server.Handler(), cookie, auth.CSRF, http.MethodPatch, "/api/v1/admin/targets/"+body.Target.ID, legacy)
	if r.Code != http.StatusOK {
		t.Fatalf("legacy edit: %d %s", r.Code, r.Body.String())
	}
	if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Target.AddressFamily != "ipv6" || body.Target.PacketCount != 3 {
		t.Fatal("legacy edit silently changed probe meaning")
	}
	for _, invalid := range []string{
		`{"name":"invalid","kind":"ping","host":"example.test","timeout_ms":3000,"address_family":"ipv6","packet_count":0}`,
		`{"name":"invalid","kind":"ping","host":"example.test","timeout_ms":3000,"address_family":"ipv6","packet_count":6}`,
		`{"name":"invalid","kind":"ping","host":"example.test","timeout_ms":3000,"address_family":""}`,
		`{"name":"invalid","kind":"ping","host":"192.0.2.53","timeout_ms":3000,"address_family":"ipv6"}`,
		`{"name":"invalid","kind":"tcping","host":"example.test","port":443,"timeout_ms":3000,"packet_count":1}`,
	} {
		if r := authenticatedRequest(t, f.server.Handler(), cookie, auth.CSRF, http.MethodPost, "/api/v1/admin/targets", invalid); r.Code != http.StatusBadRequest {
			t.Fatalf("invalid options accepted: %s", invalid)
		}
	}
	unauth := httptest.NewRequest(http.MethodPost, "/api/v1/admin/targets", bytes.NewBufferString(create))
	denied := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(denied, unauth)
	if denied.Code != http.StatusUnauthorized {
		t.Fatal("unauthenticated profile write accepted")
	}
}
