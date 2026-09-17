// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cada teste cria seu
// próprio schema de tenant isolado, mesmo padrão dos demais pacotes
// internal/*.
package views

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func testDB(t *testing.T) *database.DB {
	t.Helper()
	dsn := os.Getenv("SALTCORN_GO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SALTCORN_GO_TEST_DATABASE_URL não definida — pulando teste que exige Postgres real")
	}
	db, err := database.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("database.Open() erro inesperado: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func sanitizeForSchema(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// testFixture prepara um tenant isolado com o catálogo de metadados
// (_sc_tables) e uma tabela dinâmica "books" — o suficiente para uma view
// referenciar table_id de verdade.
func testFixture(t *testing.T, db *database.DB) (tenancy.Tenant, int) {
	t.Helper()
	ctx := context.Background()
	tenant := tenancy.Tenant(fmt.Sprintf("views_test_%s", sanitizeForSchema(t.Name())))

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
	})

	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if err := metadata.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		if err := EnsureSchema(ctx, tx); err != nil {
			return err
		}
		books, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = books.ID
		return nil
	}); err != nil {
		t.Fatalf("setup do fixture: %v", err)
	}
	return tenant, tableID
}

func TestCreateView_Success(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	var v View
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		v, err = CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", map[string]any{"above": []any{}}, ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}
	if v.Name != "booklist" || v.TableID != tableID || v.Template != "List" {
		t.Errorf("view criada = %+v", v)
	}
	if v.MinRole != identity.RoleAdmin {
		t.Errorf("MinRole = %v, esperado RoleAdmin (nunca publicada por omissão)", v.MinRole)
	}
	if v.Version == "" {
		t.Error("Version vazio, esperado xmin não vazio")
	}
}

func TestCreateView_NotAuthorizedForNonAdmin(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateView(ctx, tx, identity.RolePublic, "booklist", tableID, "List", nil, ViewOptions{})
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("CreateView com ator público = %v, esperado ErrNotAuthorized", err)
	}
}

func TestCreateView_DuplicateNameRejected(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", nil, ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("primeira CreateView: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "Show", nil, ViewOptions{})
		return err
	})
	if !errors.Is(err, ErrDuplicateName) {
		t.Errorf("segunda CreateView com nome duplicado = %v, esperado ErrDuplicateName", err)
	}
}

// TestGetView_PublishWithTwoRoles é o teste direto do critério de aceite
// "publica com dois papéis": um ator público não lê a view recém-criada
// (MinRole = RoleAdmin por padrão); depois de UpdateView baixar MinRole
// para RolePublic ("publicar"), o mesmo ator público já consegue ler —
// sem reiniciar nada, a checagem é sempre fresca (identity.CanRead a cada
// chamada, nunca cacheada, mesmo espírito de GO-015).
func TestGetView_PublishWithTwoRoles(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	// Desde GO-020, UpdateView bloqueia a publicação (MinRole != RoleAdmin)
	// de uma view cujo layout não é executável pelo runtime novo — este
	// teste valida visibilidade por papel, não renderização, então precisa
	// de um layout COMPATÍVEL (compatibleConfiguration, de
	// classify_test.go) para o passo de publicar não ser bloqueado por um
	// motivo alheio ao que o teste verifica.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText})
		if err != nil {
			return err
		}
		_, err = metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger})
		return err
	}); err != nil {
		t.Fatalf("preparar campos title/pages: %v", err)
	}

	var created View
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", compatibleConfiguration(), ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}

	// Antes de publicar: admin lê, público não.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetView(ctx, tx, identity.RoleAdmin, created.ID)
		return err
	}); err != nil {
		t.Errorf("GetView (admin, antes de publicar) = %v, esperado sucesso", err)
	}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetView(ctx, tx, identity.RolePublic, created.ID)
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("GetView (público, antes de publicar) = %v, esperado ErrNotAuthorized", err)
	}

	// Publicar: UpdateView baixando MinRole para RolePublic.
	publicRole := identity.RolePublic
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateView(ctx, tx, identity.RoleAdmin, created.ID, created.Version, ViewUpdate{MinRole: &publicRole})
		return err
	}); err != nil {
		t.Fatalf("UpdateView (publicar): %v", err)
	}

	// Depois de publicar: público também lê.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := GetView(ctx, tx, identity.RolePublic, created.ID)
		return err
	}); err != nil {
		t.Errorf("GetView (público, depois de publicar) = %v, esperado sucesso", err)
	}
}

// TestUpdateView_ConcurrentEditConflict é o teste direto do critério de
// aceite "conflito de edição é apresentado sem sobrescrever
// silenciosamente": duas "sessões" leem a mesma view (mesma Version),
// ambas editam; a segunda a chegar recebe ErrVersionConflict, nunca
// sobrescreve o que a primeira gravou.
func TestUpdateView_ConcurrentEditConflict(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	var created View
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", map[string]any{"above": []any{"v1"}}, ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}

	// "Sessão A" lê created.Version, edita e salva com sucesso.
	configA := map[string]any{"above": []any{"editado por A"}}
	var afterA View
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		afterA, err = UpdateView(ctx, tx, identity.RoleAdmin, created.ID, created.Version, ViewUpdate{Configuration: &configA})
		return err
	}); err != nil {
		t.Fatalf("UpdateView (sessão A): %v", err)
	}

	// "Sessão B" leu a MESMA Version original (antes de A salvar) e tenta
	// salvar por cima — deve ser rejeitada, não sobrescrever A.
	configB := map[string]any{"above": []any{"editado por B, deveria ser rejeitado"}}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateView(ctx, tx, identity.RoleAdmin, created.ID, created.Version, ViewUpdate{Configuration: &configB})
		return err
	})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("UpdateView (sessão B, versão obsoleta) = %v, esperado ErrVersionConflict", err)
	}

	// Confirma que o conteúdo de A sobrevive intacto — B não sobrescreveu nada.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		reread, err := GetView(ctx, tx, identity.RoleAdmin, created.ID)
		if err != nil {
			return err
		}
		aboveList, _ := reread.Configuration["above"].([]any)
		if len(aboveList) != 1 || aboveList[0] != "editado por A" {
			t.Errorf("configuration após conflito = %+v, esperado só a edição de A (%+v)", reread.Configuration, afterA.Configuration)
		}
		return nil
	}); err != nil {
		t.Fatalf("releitura final: %v", err)
	}
}

func TestUpdateView_NotFoundVsConflict(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	var created View
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", nil, ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("CreateView: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := UpdateView(ctx, tx, identity.RoleAdmin, created.ID+999, created.Version, ViewUpdate{})
		return err
	})
	if !errors.Is(err, ErrViewNotFound) {
		t.Errorf("UpdateView (id inexistente) = %v, esperado ErrViewNotFound", err)
	}
}

func TestListViews_FiltersByRoleAndTable(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	ctx := context.Background()

	// "published" nasce já pública (MinRole na criação, não via UpdateView)
	// — desde GO-020 isso também exige um layout compatível (ver
	// CreateView); "draft" fica admin-only e por isso pode ter
	// configuration vazia sem ser bloqueada.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, tableID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger}); err != nil {
			return err
		}
		if _, err := CreateView(ctx, tx, identity.RoleAdmin, "published", tableID, "List", compatibleConfiguration(), ViewOptions{MinRole: identity.RolePublic}); err != nil {
			return err
		}
		if _, err := CreateView(ctx, tx, identity.RoleAdmin, "draft", tableID, "List", nil, ViewOptions{}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var asPublic, asAdmin []View
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		asPublic, err = ListViews(ctx, tx, identity.RolePublic, tableID)
		return err
	}); err != nil {
		t.Fatalf("ListViews (público): %v", err)
	}
	if len(asPublic) != 1 || asPublic[0].Name != "published" {
		t.Errorf("ListViews (público) = %+v, esperado só 'published'", asPublic)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		asAdmin, err = ListViews(ctx, tx, identity.RoleAdmin, tableID)
		return err
	}); err != nil {
		t.Fatalf("ListViews (admin): %v", err)
	}
	if len(asAdmin) != 2 {
		t.Errorf("ListViews (admin) = %+v, esperado as duas views", asAdmin)
	}
}
