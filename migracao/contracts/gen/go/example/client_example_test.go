package example

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListFirstPage_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer good-token" {
			t.Errorf("Authorization header = %q, esperado Bearer good-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"items":[{"id":1,"version":1,"created_at":"2026-01-15T00:00:00Z"}],"next_cursor":null}`))
	}))
	defer srv.Close()

	items, cursor, err := ListFirstPage(context.Background(), srv.URL, "good-token", "acme", "guitars")
	if err != nil {
		t.Fatalf("ListFirstPage() erro inesperado: %v", err)
	}
	if len(items) != 1 || items[0].Id != 1 {
		t.Errorf("items = %+v, esperado 1 item com id=1", items)
	}
	if cursor != nil {
		t.Errorf("cursor = %v, esperado nil (última página)", cursor)
	}
}

// TestListFirstPage_Unauthorized cobre o exemplo negativo de identidade
// delegada inválida (mesmo formato do exemplo documentado em
// migracao/contracts/openapi/common.yaml e fixtures/identidade-delegada.md).
func TestListFirstPage_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":"invalid_identity_token","message":"assinatura do token de identidade delegada não confere"}}`))
	}))
	defer srv.Close()

	_, _, err := ListFirstPage(context.Background(), srv.URL, "forged-token", "acme", "guitars")
	if err == nil {
		t.Fatal("esperava erro para token com assinatura inválida, obteve nil")
	}
}

// TestListFirstPage_TenantMismatch cobre o segundo exemplo negativo:
// identidade válida, mas o tenant do token não corresponde ao recurso.
func TestListFirstPage_TenantMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"code":"tenant_mismatch","message":"tenant do token de identidade não corresponde ao tenant do recurso"}}`))
	}))
	defer srv.Close()

	_, _, err := ListFirstPage(context.Background(), srv.URL, "valid-but-wrong-tenant-token", "other-tenant", "guitars")
	if err == nil {
		t.Fatal("esperava erro para tenant divergente, obteve nil")
	}
}

func TestCreateOne_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Idempotency-Key"); got != "idem-123" {
			t.Errorf("Idempotency-Key header = %q, esperado idem-123", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42,"version":1,"created_at":"2026-01-15T00:00:00Z"}`))
	}))
	defer srv.Close()

	rec, err := CreateOne(context.Background(), srv.URL, "good-token", "acme", "guitars", "idem-123", map[string]any{"name": "Stratocaster"})
	if err != nil {
		t.Fatalf("CreateOne() erro inesperado: %v", err)
	}
	if rec.Id != 42 {
		t.Errorf("Id = %d, esperado 42", rec.Id)
	}
}

func TestGetBootstrap_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"actor":{"id":1,"role_id":1},"tenant":"acme"}`))
	}))
	defer srv.Close()

	boot, err := GetBootstrap(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetBootstrap() erro inesperado: %v", err)
	}
	if boot.Tenant != "acme" {
		t.Errorf("Tenant = %q, esperado acme", boot.Tenant)
	}
}
