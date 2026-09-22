// Testes HTTP de versionamento de linha (GO-045) — exigem Postgres real,
// pulam (t.Skip) se SALTCORN_GO_TEST_DATABASE_URL não estiver definida,
// mesmo padrão de records_test.go.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

// newVersionedFixture é como newTestFixture (records_test.go), mas cria
// a tabela "posts" com Versioned=true e um único campo de texto "title"
// — a fixture destes testes.
func newVersionedFixture(t *testing.T, db *database.DB) testFixture {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("srv_test_%s", sanitizeForSchema(t.Name())))

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS %s`, pgx.Identifier{string(tenant)}.Sanitize()))
		return err
	}); err != nil {
		t.Fatalf("criar schema de teste: %v", err)
	}
	t.Cleanup(func() {
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, pgx.Identifier{string(tenant)}.Sanitize()))
			return err
		})
		_ = db.WithTenant(context.Background(), "public", func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, "DELETE FROM _sc_capability_ownership WHERE tenant = $1 AND capability = $2", string(tenant), recordsCapability)
			return err
		})
	})

	var userID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := identity.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := metadata.EnsureSchema(ctx, database.AsTx(tx)); err != nil {
			return err
		}
		if err := outbox.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := triggers.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		hash, err := identity.HashPassword("hunter2")
		if err != nil {
			return err
		}
		userID, err = identity.CreateUser(ctx, tx, "ator@example.com", hash, identity.RoleAdmin)
		if err != nil {
			return err
		}
		posts, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "posts", metadata.TableOptions{Versioned: true})
		if err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, posts.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true})
		return err
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}

	if err := db.WithTenant(ctx, "public", func(ctx context.Context, tx pgx.Tx) error {
		return cutover.EnsureSchema(ctx, tx)
	}); err != nil {
		t.Fatalf("cutover.EnsureSchema: %v", err)
	}
	guard := cutover.NewGuard()
	if err := cutover.SwitchOwner(ctx, db, guard, tenant, recordsCapability, cutover.OwnerGo, 2*time.Second); err != nil {
		t.Fatalf("cutover.SwitchOwner: %v", err)
	}

	return testFixture{tenant: tenant, userID: userID, guard: guard, tracker: shutdown.NewTracker()}
}

func TestVersionedTable_HTTPCreateUpdateHistoryRestore(t *testing.T) {
	db := testDB(t)
	fx := newVersionedFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)
	dispatcher := &triggers.Dispatcher{Actions: triggers.BuiltinActions()}

	createHandler := buildRecordsHandler(t, verifier, fx.guard, createRecordHandler(fx.tracker, db, dispatcher))
	body := bytes.NewBufferString(`{"title":"v1"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/tables/posts/records", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "posts")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", "create-post-1")
	rec := httptest.NewRecorder()
	createHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, corpo = %s, esperado 201", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decodificar create: %v", err)
	}
	id := int(created["id"].(float64))
	version := created["_version"].(string)

	updateHandler := buildRecordsHandler(t, verifier, fx.guard, updateRecordHandler(fx.tracker, db, dispatcher))
	updBody := bytes.NewBufferString(fmt.Sprintf(`{"title":"v2","_version":%q}`, version))
	updReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/v1/tenants/%s/tables/posts/records/%d", fx.tenant, id), updBody)
	updReq.SetPathValue("tenant", string(fx.tenant))
	updReq.SetPathValue("table", "posts")
	updReq.SetPathValue("id", strconv.Itoa(id))
	updReq.Header.Set("Authorization", "Bearer "+token)
	updReq.Header.Set("Idempotency-Key", "update-post-1")
	updRec := httptest.NewRecorder()
	updateHandler.ServeHTTP(updRec, updReq)
	if updRec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, corpo = %s, esperado 200", updRec.Code, updRec.Body.String())
	}

	historyHandler := buildRecordsHandler(t, verifier, fx.guard, getRecordHistoryHandler(fx.tracker, db))
	histReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/tenants/%s/tables/posts/records/%d/history", fx.tenant, id), nil)
	histReq.SetPathValue("tenant", string(fx.tenant))
	histReq.SetPathValue("table", "posts")
	histReq.SetPathValue("id", strconv.Itoa(id))
	histReq.Header.Set("Authorization", "Bearer "+token)
	histRec := httptest.NewRecorder()
	historyHandler.ServeHTTP(histRec, histReq)
	if histRec.Code != http.StatusOK {
		t.Fatalf("history: status = %d, corpo = %s, esperado 200", histRec.Code, histRec.Body.String())
	}
	var histBody struct {
		Versions []recordHistoryVersionResponse `json:"versions"`
	}
	if err := json.Unmarshal(histRec.Body.Bytes(), &histBody); err != nil {
		t.Fatalf("decodificar history: %v", err)
	}
	if len(histBody.Versions) != 2 {
		t.Fatalf("versões = %d, esperado 2 (create+update)", len(histBody.Versions))
	}
	if histBody.Versions[0].Version != 2 || histBody.Versions[0].Record["title"] != "v2" {
		t.Errorf("versão mais recente = %+v, esperado version=2 title=v2", histBody.Versions[0])
	}

	restoreHandler := buildRecordsHandler(t, verifier, fx.guard, restoreRecordVersionHandler(fx.tracker, db))
	restoreReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v1/tenants/%s/tables/posts/records/%d/restore", fx.tenant, id), bytes.NewBufferString(`{"version":1}`))
	restoreReq.SetPathValue("tenant", string(fx.tenant))
	restoreReq.SetPathValue("table", "posts")
	restoreReq.SetPathValue("id", strconv.Itoa(id))
	restoreReq.Header.Set("Authorization", "Bearer "+token)
	restoreRec := httptest.NewRecorder()
	restoreHandler.ServeHTTP(restoreRec, restoreReq)
	if restoreRec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d, corpo = %s, esperado 200", restoreRec.Code, restoreRec.Body.String())
	}
	var restored map[string]any
	if err := json.Unmarshal(restoreRec.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decodificar restore: %v", err)
	}
	if restored["title"] != "v1" {
		t.Errorf("title após restore = %v, esperado \"v1\"", restored["title"])
	}
}

func TestGetRecordHistoryHandler_UnversionedTable_Returns409(t *testing.T) {
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin) // "widgets", sem Versioned.
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	token := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.userID), fx.tenant, time.Minute)

	handler := buildRecordsHandler(t, verifier, fx.guard, getRecordHistoryHandler(fx.tracker, db))
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/tenants/%s/tables/widgets/records/1/history", fx.tenant), nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("table", "widgets")
	req.SetPathValue("id", "1")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, corpo = %s, esperado 409", rec.Code, rec.Body.String())
	}
}
