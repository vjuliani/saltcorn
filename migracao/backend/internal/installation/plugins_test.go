package installation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

func TestEngineCompatible(t *testing.T) {
	cases := []struct {
		constraint, version string
		want                bool
	}{
		{"1.0.0", "1.0.0", true},
		{"1.0.0", "1.0.1", false},
		{"^1.0.0", "1.9.9", true},
		{"^1.0.0", "2.0.0", false},
		{"~1.2.0", "1.2.9", true},
		{"~1.2.0", "1.3.0", false},
		{">=1.0.0", "1.0.0", true},
		{">=1.0.0 <2.0.0", "1.5.0", true},
		{">=1.0.0 <2.0.0", "2.0.0", false},
		{">1.0.0", "1.0.0", false},
		{"<=1.0.0", "1.0.0", true},
		{"<1.0.0", "1.0.0", false},
	}
	for _, tc := range cases {
		got, err := engineCompatible(tc.constraint, tc.version)
		if err != nil {
			t.Fatalf("engineCompatible(%q,%q): %v", tc.constraint, tc.version, err)
		}
		if got != tc.want {
			t.Errorf("engineCompatible(%q,%q) = %v, want %v", tc.constraint, tc.version, got, tc.want)
		}
	}
	if _, err := engineCompatible("^1.0.0", "1.0"); err == nil {
		t.Fatal("esperado erro para versão semver inválida")
	}
	if _, err := engineCompatible("^abc", "1.0.0"); err == nil {
		t.Fatal("esperado erro para restrição de engine inválida")
	}
}

func writePlugin(t *testing.T, root, name, version, engineRange string) {
	t.Helper()
	dir := filepath.Join(root, "plugins", name)
	must(t, os.MkdirAll(dir, 0700))
	pkg := map[string]any{"name": name, "version": version}
	if engineRange != "" {
		pkg["engines"] = map[string]string{"saltcorn": engineRange}
	}
	b, err := json.Marshal(pkg)
	must(t, err)
	must(t, os.WriteFile(filepath.Join(dir, "package.json"), b, 0600))
	must(t, os.WriteFile(filepath.Join(dir, "index.js"), []byte("module.exports={}"), 0600))
}

func TestSyncPlugins_RejectsIncompatibleEngine(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	writePlugin(t, root, "future-plugin", "1.0.0", ">=99.0.0")
	if SyncPlugins(ctx, root, c) == nil {
		t.Fatal("esperado erro: engine declarada incompatível com PluginEngineVersion")
	}
}

func TestSyncPlugins_RejectsInvalidSemver(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	writePlugin(t, root, "bad-version-plugin", "not-a-version", "")
	if SyncPlugins(ctx, root, c) == nil {
		t.Fatal("esperado erro: versão semver inválida")
	}
}

func TestSyncPlugins_AcceptsCompatibleEngine(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	writePlugin(t, root, "compatible-plugin", "1.0.0", "^1.0.0")
	must(t, SyncPlugins(ctx, root, c))
}

// TestSyncPlugins_UpgradeAuditTrail prova o caso de uso real de
// _sc_metadata para plugin_upgrade: instalar, depois trocar de versão,
// depois remover — cada mudança real gera EXATAMENTE uma entrada; um
// sync repetido sem nenhuma mudança não gera entrada nova (idempotente).
func TestSyncPlugins_UpgradeAuditTrail(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")

	writePlugin(t, root, "audited-plugin", "1.0.0", "")
	must(t, SyncPlugins(ctx, root, c))
	must(t, SyncPlugins(ctx, root, c)) // repetir sem mudança não deve duplicar

	must(t, os.RemoveAll(filepath.Join(root, "plugins", "audited-plugin")))
	writePlugin(t, root, "audited-plugin", "2.0.0", "")
	must(t, SyncPlugins(ctx, root, c))

	must(t, os.RemoveAll(filepath.Join(root, "plugins", "audited-plugin")))
	must(t, SyncPlugins(ctx, root, c))

	must(t, transaction(ctx, root, c, func(tx database.Tx, _ pgx.Tx) error {
		entries, err := ListMetadata(ctx, tx, "plugin_upgrade")
		if err != nil {
			return err
		}
		if len(entries) != 3 {
			t.Fatalf("esperado 3 eventos de upgrade (instalar 1.0.0, atualizar para 2.0.0, remover), obtido %d", len(entries))
		}
		var removed, upgraded, installed bool
		for _, e := range entries {
			var body map[string]any
			must(t, json.Unmarshal(e.Body, &body))
			switch {
			case body["from_version"] == nil && body["to_version"] == "1.0.0":
				installed = true
			case body["from_version"] == "1.0.0" && body["to_version"] == "2.0.0":
				upgraded = true
			case body["from_version"] == "2.0.0" && body["to_version"] == nil:
				removed = true
			}
		}
		if !installed || !upgraded || !removed {
			t.Fatalf("eventos incompletos: installed=%v upgraded=%v removed=%v", installed, upgraded, removed)
		}
		return nil
	}))
}
