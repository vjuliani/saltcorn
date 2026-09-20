package telemetry

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdmissionRejectsWithoutQueueAndRecovers(t *testing.T) {
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	handler := LimitRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}), 1, 100*time.Millisecond, NewRegistry())
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/test", nil))
		close(done)
	}()
	<-entered
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, httptest.NewRequest("GET", "/v1/test", nil))
	if rejected.Code != 503 {
		t.Fatalf("status %d", rejected.Code)
	}
	healthy := httptest.NewRecorder()
	handler.ServeHTTP(healthy, httptest.NewRequest("GET", "/healthz", nil))
	if healthy.Code != 200 {
		t.Fatal("health unavailable")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadline failed")
	}
	close(release)
	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, httptest.NewRequest("GET", "/v1/test", nil))
	if recovered.Code != 200 {
		t.Fatal("capacity not released after timeout")
	}
}
