package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func TestSyncMobileE2E(t *testing.T) {
	if os.Getenv("SALTCORN_GO_TEST_MOBILE_E2E") != "1" {
		t.Skip("exige Node 22 e BFF compilado; CI mobile-sync define SALTCORN_GO_TEST_MOBILE_E2E=1")
	}
	db := testDB(t)
	fx := newTestFixture(t, db, identity.RoleAdmin)
	var otherID int
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		otherID, err = identity.CreateUser(ctx, tx, "other@example.test", "unused", identity.RoleAdmin)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/tenants/{tenant}/sync/{table}/exchange", buildRecordsHandler(t, verifier, fx.guard, syncExchangeHandler(fx.tracker, db)))
	// Test-only fixture operations, never registered in production main.go.
	mux.HandleFunc("POST /fixture/schema", func(w http.ResponseWriter, r *http.Request) {
		err := db.WithTenant(r.Context(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
			table, err := metadata.GetTable(ctx, database.AsTx(tx), "widgets")
			if err != nil {
				return err
			}
			_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, table.ID, metadata.FieldDef{Name: "note", Type: metadata.FieldText})
			return err
		})
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /fixture/revoke", func(w http.ResponseWriter, r *http.Request) {
		err := db.WithTenant(r.Context(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "UPDATE _sc_tables SET min_role_read=1 WHERE name='widgets'"); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE _sc_users SET role_id=80 WHERE id=$1", fx.userID)
			return err
		})
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(204)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	config, _ := json.Marshal(map[string]string{"url": server.URL, "tenant": string(fx.tenant), "actor": strconv.Itoa(fx.userID), "otherActor": strconv.Itoa(otherID), "secret": testServiceIdentitySecret, "dir": t.TempDir()})
	script, err := filepath.Abs("../../../packages/mobile-sync-test/http-e2e.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", script)
	cmd.Env = append(os.Environ(), "GO031_FIXTURE="+string(config))
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
