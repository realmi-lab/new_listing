package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"listing-tracker/internal/tracker"
)

func TestLoopbackListenerRefusesOtherHosts(t *testing.T) {
	server := New(tracker.New(nil, time.Hour), nil, time.Now, nil)
	server.LoopbackOnly("8770")
	handler := server.Handler()
	for host, want := range map[string]int{
		"127.0.0.1:8770":        http.StatusOK,
		"localhost:8770":        http.StatusOK,
		"evil.example:8770":     http.StatusForbidden,
		"127.0.0.1:9999":        http.StatusForbidden,
		"rebound.attacker.test": http.StatusForbidden,
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/snapshot", nil)
		request.Host = host
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != want {
			t.Errorf("Host %s: status %d, want %d", host, recorder.Code, want)
		}
	}
}

func TestOnlyReadMethodsAreServed(t *testing.T) {
	handler := New(tracker.New(nil, time.Hour), nil, time.Now, nil).Handler()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		request := httptest.NewRequest(method, "/api/snapshot", nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/snapshot: status %d", method, recorder.Code)
		}
	}
}
