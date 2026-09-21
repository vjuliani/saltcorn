// Testes de PATCH .../tables/{table}/permissions (GO-044) — exigem
// Postgres real, reaproveita os helpers de records_test.go.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func TestUpdateTablePermissionsHandler_Success(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	handler := buildRecordsHandler(t, verifier, fx.guard, updateTablePermissionsHandler(fx.tracker, db))
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	body := strings.NewReader(`{"min_role_read":1,"min_role_write":1}`)
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/permissions", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, corpo = %s, esperado 200", rec.Code, rec.Body.String())
	}
	var resp tableResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if resp.MinRoleRead != 1 || resp.MinRoleWrite != 1 {
		t.Errorf("resp = %+v, esperado min_role_read=1 min_role_write=1", resp)
	}

	// Confirma que a leitura pública, que passava antes (fixture cria
	// "widgets" com RolePublic), agora é negada — a mutação teve efeito
	// real, não só na resposta. O ator público precisa existir no MESMO
	// tenant do fixture (fx.tenant), não um tenant novo.
	var publicUserID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		publicUserID, err = identity.CreateUser(ctx, tx, "publico@example.com", hash, identity.RolePublic)
		return err
	}); err != nil {
		t.Fatalf("criar usuário público: %v", err)
	}

	listHandler := buildRecordsHandler(t, verifier, fx.guard, listRecordsHandler(fx.tracker, db))
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(publicUserID), fx.tenant, time.Minute)
	listReq := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/tables/widgets/records", nil)
	listReq.SetPathValue("tenant", string(fx.tenant))
	listReq.SetPathValue("table", "widgets")
	listReq.Header.Set("Authorization", "Bearer "+publicToken)
	listRec := httptest.NewRecorder()
	listHandler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusForbidden {
		t.Errorf("leitura pública após restringir permissão: status = %d, esperado 403", listRec.Code)
	}
}
