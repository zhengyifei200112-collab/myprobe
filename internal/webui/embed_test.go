package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAndSPAFallback(t *testing.T) {
	h := NewHandler()
	for _, target := range []string{"/", "/admin"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", target, response.Code)
		}
		if !strings.Contains(response.Body.String(), `<div id="app"></div>`) {
			t.Fatalf("%s did not return the SPA index", target)
		}
	}
}

func TestHandlerRejectsMissingAsset(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
	response := httptest.NewRecorder()
	NewHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}
