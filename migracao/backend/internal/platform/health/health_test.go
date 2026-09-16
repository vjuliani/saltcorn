package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLivenessHandler_AlwaysOK(t *testing.T) {
	c := &Checker{}
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	c.LivenessHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado %d", rec.Code, http.StatusOK)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("corpo inválido: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status no corpo = %q, esperado ok", body["status"])
	}
}

func TestReadinessHandler_NotReadyByDefault(t *testing.T) {
	c := &Checker{}
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	c.ReadinessHandler()(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado %d (não pronto por padrão)", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadinessHandler_ReadyAfterSetReady(t *testing.T) {
	c := &Checker{}
	c.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	c.ReadinessHandler()(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, esperado %d após SetReady(true)", rec.Code, http.StatusOK)
	}

	c.SetReady(false)
	rec2 := httptest.NewRecorder()
	c.ReadinessHandler()(rec2, req)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, esperado %d após SetReady(false)", rec2.Code, http.StatusServiceUnavailable)
	}
}
