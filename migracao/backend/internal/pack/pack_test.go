// Corpus de export/import exigido pelo critério de aceite de GO-027:
// "export legado importa no Go e reexporta sem perda no corpus; faltas
// de plugin/versão são reportadas antes de aplicar alterações." Contra
// Postgres real.
package pack

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/library"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/scheduler"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

// buildSampleApp cria uma aplicação representativa: duas tabelas com uma
// relação (FieldKey), uma view, um trigger, um trigger agendado, um item
// de biblioteca e configuração — o "corpus" do critério de aceite.
func buildSampleApp(t *testing.T, db *database.DB, tenant tenancy.Tenant) {
	t.Helper()
	ctx := context.Background()
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		authors, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "authors", metadata.TableOptions{
			MinRoleRead: identity.RolePublic, MinRoleWrite: identity.RoleAdmin,
		})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, authors.ID, metadata.FieldDef{Name: "name", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}

		books, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{
			MinRoleRead: identity.RolePublic, MinRoleWrite: identity.RoleAdmin,
		})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, books.ID, metadata.FieldDef{Name: "author", Type: metadata.FieldKey, References: "authors"}); err != nil {
			return err
		}

		// MinRole fica RoleAdmin (rascunho, não publicada) de propósito: a
		// configuration usada aqui não precisa ser compatível com o runtime
		// de renderização de GO-020 (ClassifyView só valida layout quando a
		// view É publicada) — este teste exercita export/import, não
		// renderização.
		if _, err := views.CreateView(ctx, tx, identity.RoleAdmin, "books_list", books.ID, "List", map[string]any{
			"columns": []any{map[string]any{"field_name": "title"}},
		}, views.ViewOptions{MinRole: identity.RoleAdmin}); err != nil {
			return err
		}

		if _, err := triggers.CreateTrigger(ctx, tx, triggers.Trigger{TableID: books.ID, When: triggers.WhenInsert, Action: "log"}); err != nil {
			return err
		}

		if _, err := scheduler.CreateScheduledTrigger(ctx, tx, "limpeza-noturna", "log", "0 0 * * *", "UTC"); err != nil {
			return err
		}

		if _, err := library.CreateOrReplace(ctx, tx, "cabecalho", "star", map[string]any{"type": "besides"}); err != nil {
			return err
		}

		if err := config.Set(ctx, tx, "site_name", "Livraria Exemplo"); err != nil {
			return err
		}
		return config.Set(ctx, tx, "menu_items", []any{"Início", "Sobre"})
	}); err != nil {
		t.Fatalf("buildSampleApp: %v", err)
	}
}

func exportPack(t *testing.T, db *database.DB, tenant tenancy.Tenant, plugins []PluginDependency) Pack {
	t.Helper()
	var p Pack
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		p, err = Export(ctx, tx, identity.RoleAdmin, plugins)
		return err
	}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	return p
}

// TestRoundTrip_ExportImportExport_NoLoss é o critério de aceite central
// desta tarefa: exporta uma aplicação real, importa numa tenant NOVA
// (vazia), reexporta essa tenant, e compara byte a byte (via JSON
// canônico) o resultado — nenhuma entidade, campo, configuração ou item
// de biblioteca pode se perder ou mudar no caminho.
func TestRoundTrip_ExportImportExport_NoLoss(t *testing.T) {
	db := testDB(t)
	source := newTenant(t, db, "source")
	buildSampleApp(t, db, source)

	plugins := []PluginDependency{{Name: "base-plugin", Version: "1.7.0-alpha.1"}}
	original := exportPack(t, db, source, plugins)

	// Verificação de sanidade: o corpus realmente tem conteúdo em cada
	// categoria — um teste que passasse comparando dois packs VAZIOS não
	// provaria nada.
	if len(original.Tables) != 2 || len(original.Views) != 1 || len(original.Triggers) != 1 ||
		len(original.ScheduledTriggers) != 1 || len(original.Library) != 1 || len(original.Config) != 2 {
		t.Fatalf("pack original incompleto, corpus não cobre todas as categorias: %+v", original)
	}

	dest := newTenant(t, db, "dest")
	if err := db.WithTenant(context.Background(), dest, func(ctx context.Context, tx pgx.Tx) error {
		return Import(ctx, tx, identity.RoleAdmin, original, map[string]string{"base-plugin": "1.7.0-alpha.1"})
	}); err != nil {
		t.Fatalf("Import: %v", err)
	}

	reexported := exportPack(t, db, dest, plugins)

	originalJSON, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal(original): %v", err)
	}
	reexportedJSON, err := json.MarshalIndent(reexported, "", "  ")
	if err != nil {
		t.Fatalf("json.Marshal(reexported): %v", err)
	}
	if string(originalJSON) != string(reexportedJSON) {
		t.Fatalf("reexport difere do pack original — perda ou alteração no round-trip:\n--- original ---\n%s\n--- reexportado ---\n%s", originalJSON, reexportedJSON)
	}
}

// TestImport_MissingPlugin_ReportsBeforeApplying_NothingCreated prova o
// segundo critério de aceite: uma dependência de plugin ausente é
// reportada e NENHUMA entidade do pack é criada — nem a tabela que não
// tem nada a ver com o plugin ausente.
func TestImport_MissingPlugin_ReportsBeforeApplying_NothingCreated(t *testing.T) {
	db := testDB(t)
	tenant := newTenant(t, db, "missing-plugin")

	p := Pack{
		Version: Version,
		Tables:  []TablePack{{Name: "orfa", MinRoleRead: identity.RolePublic, MinRoleWrite: identity.RoleAdmin}},
		Plugins: []PluginDependency{{Name: "stripe-payments", Version: "2.0"}},
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Import(ctx, tx, identity.RoleAdmin, p, map[string]string{})
	})
	var missingErr *MissingDependenciesError
	if !errors.As(err, &missingErr) {
		t.Fatalf("err = %v, esperado *MissingDependenciesError", err)
	}
	if !errors.Is(err, ErrMissingDependencies) {
		t.Fatal("errors.Is(err, ErrMissingDependencies) = false")
	}
	if len(missingErr.Missing) != 1 || missingErr.Missing[0].Name != "stripe-payments" {
		t.Fatalf("Missing = %+v, esperado 1 entrada para stripe-payments", missingErr.Missing)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		tables, err := metadata.ListTables(ctx, tx)
		if err != nil {
			return err
		}
		if len(tables) != 0 {
			t.Errorf("ListTables = %v, esperado vazio — nenhuma entidade deveria ter sido criada", tables)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestImport_PluginVersionMismatch_Reported(t *testing.T) {
	db := testDB(t)
	tenant := newTenant(t, db, "version-mismatch")

	p := Pack{Version: Version, Plugins: []PluginDependency{{Name: "core", Version: "2.0"}}}
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Import(ctx, tx, identity.RoleAdmin, p, map[string]string{"core": "1.0"})
	})
	var missingErr *MissingDependenciesError
	if !errors.As(err, &missingErr) {
		t.Fatalf("err = %v, esperado *MissingDependenciesError", err)
	}
	if len(missingErr.Missing) != 1 || missingErr.Missing[0].AvailableVersion != "1.0" {
		t.Fatalf("Missing = %+v, esperado AvailableVersion=1.0", missingErr.Missing)
	}
}

// TestImport_AtomicFailure_RollsBackEverything prova "tudo ou nada": um
// pack com uma tabela válida MAIS uma view referenciando uma tabela
// inexistente falha, e a tabela válida NÃO sobrevive ao rollback da
// transação inteira — nunca uma aplicação parcial.
func TestImport_AtomicFailure_RollsBackEverything(t *testing.T) {
	db := testDB(t)
	tenant := newTenant(t, db, "atomic")

	p := Pack{
		Version: Version,
		Tables:  []TablePack{{Name: "tabela_valida", MinRoleRead: identity.RolePublic, MinRoleWrite: identity.RoleAdmin}},
		Views:   []ViewPack{{Name: "view_quebrada", TableName: "nao_existe", Template: "List", MinRole: identity.RolePublic}},
	}

	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Import(ctx, tx, identity.RoleAdmin, p, nil)
	})
	if err == nil {
		t.Fatal("esperado erro (view referencia tabela inexistente)")
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		tables, err := metadata.ListTables(ctx, tx)
		if err != nil {
			return err
		}
		if len(tables) != 0 {
			t.Errorf("ListTables = %v, esperado vazio — tabela_valida não deveria ter sobrevivido ao rollback", tables)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

func TestImport_UnknownVersion_Rejected(t *testing.T) {
	db := testDB(t)
	tenant := newTenant(t, db, "future-version")

	p := Pack{Version: Version + 1, Tables: []TablePack{{Name: "x"}}}
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Import(ctx, tx, identity.RoleAdmin, p, nil)
	})
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, esperado ErrUnsupportedVersion", err)
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		tables, err := metadata.ListTables(ctx, tx)
		if err != nil {
			return err
		}
		if len(tables) != 0 {
			t.Errorf("ListTables = %v, esperado vazio", tables)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}
}

// TestImport_ForwardTableReference_TwoPhaseFieldCreation prova por que
// Import cria TODAS as tabelas antes de adicionar QUALQUER campo: um
// pack pode ter uma tabela cujo FieldKey referencia outra tabela
// declarada DEPOIS dela no mesmo array (aqui, "posts" referencia
// "people", que só aparece em segundo lugar) — um export real de uma
// aplicação com relações não garante nenhuma ordem topológica particular
// entre as tabelas.
func TestImport_ForwardTableReference_TwoPhaseFieldCreation(t *testing.T) {
	db := testDB(t)
	tenant := newTenant(t, db, "forward-ref")

	p := Pack{
		Version: Version,
		Tables: []TablePack{
			{
				Name: "posts", MinRoleRead: identity.RolePublic, MinRoleWrite: identity.RoleAdmin,
				Fields: []FieldPack{
					{Name: "title", Type: metadata.FieldText},
					{Name: "author", Type: metadata.FieldKey, References: "people"},
				},
			},
			{
				Name: "people", MinRoleRead: identity.RolePublic, MinRoleWrite: identity.RoleAdmin,
				Fields: []FieldPack{{Name: "name", Type: metadata.FieldText}},
			},
		},
	}

	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return Import(ctx, tx, identity.RoleAdmin, p, nil)
	}); err != nil {
		t.Fatalf("Import (referência para tabela declarada depois): %v", err)
	}
}

func TestValidate_NoDependencies_ReturnsEmpty(t *testing.T) {
	if got := Validate(Pack{}, nil); len(got) != 0 {
		t.Fatalf("Validate(pack sem plugins) = %v, esperado vazio", got)
	}
}

func TestExport_EmptyTenant_ReturnsEmptyPack(t *testing.T) {
	db := testDB(t)
	tenant := newTenant(t, db, "empty")

	p := exportPack(t, db, tenant, nil)
	if p.Version != Version {
		t.Errorf("Version = %d, esperado %d", p.Version, Version)
	}
	if len(p.Tables) != 0 || len(p.Views) != 0 || len(p.Triggers) != 0 ||
		len(p.ScheduledTriggers) != 0 || len(p.Library) != 0 || len(p.Config) != 0 {
		t.Errorf("Export de tenant vazia não é vazio: %+v", p)
	}
}
