// Testes deste arquivo exigem Postgres real — reaproveitam
// testDB/testTenant/seedCorpus/runQuery de compiler_test.go (mesmo
// pacote). O corpus books/publisher é o mesmo shape de GO-001/GO-002.
package records

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// seedUniqueTable cria uma tabela "authors" com um campo "email" único e
// obrigatório — usada só pelos testes de violação de unicidade, separada
// do corpus books/publisher para não alterar seu shape (outros testes de
// GO-012 dependem dele exatamente como está).
func seedUniqueTable(t *testing.T, db *database.DB, tenant tenancy.Tenant) int {
	t.Helper()
	var tableID int
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		tbl, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "authors", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = tbl.ID
		_, err = metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tbl.ID, metadata.FieldDef{Name: "email", Type: metadata.FieldText, Required: true, Unique: true})
		return err
	}); err != nil {
		t.Fatalf("seedUniqueTable: %v", err)
	}
	return tableID
}

func runCreate(t *testing.T, db *database.DB, tenant tenancy.Tenant, actorRole identity.RoleID, table string, values map[string]any, hooks *Hooks) (map[string]any, error) {
	t.Helper()
	var record map[string]any
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		record, err = CreateRecord(ctx, tx, actorRole, table, values, hooks)
		return err
	})
	return record, err
}

func runUpdate(t *testing.T, db *database.DB, tenant tenancy.Tenant, actorRole identity.RoleID, table string, id int, version string, values map[string]any, hooks *Hooks) (map[string]any, error) {
	t.Helper()
	var record map[string]any
	err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		record, err = UpdateRecord(ctx, tx, actorRole, table, id, version, values, hooks)
		return err
	})
	return record, err
}

func runDelete(t *testing.T, db *database.DB, tenant tenancy.Tenant, actorRole identity.RoleID, table string, id int, version string, hooks *Hooks) error {
	t.Helper()
	return db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		return DeleteRecord(ctx, tx, actorRole, table, id, version, hooks)
	})
}

func TestCreateRecord_Success(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	record, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "Ada Lovelace", "pages": 320}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if record["author"] != "Ada Lovelace" {
		t.Errorf("author = %v", record["author"])
	}
	if record["_version"] == nil || record["_version"] == "" {
		t.Error("_version ausente no registro criado")
	}
	if record["id"] == nil {
		t.Error("id ausente no registro criado")
	}
}

func TestCreateRecord_UnknownFieldRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"nao_existe": "x", "pages": 1}, nil)
	if !errors.Is(err, ErrUnknownField) {
		t.Errorf("campo desconhecido = %v, esperado ErrUnknownField", err)
	}
}

func TestCreateRecord_TypeMismatchRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "A", "pages": "not-a-number"}, nil)
	if !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("tipo incompatível = %v, esperado ErrTypeMismatch", err)
	}
}

func TestCreateRecord_RequiredFieldMissing(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "A"}, nil) // "pages" é required, ausente
	if !errors.Is(err, ErrRequiredField) {
		t.Errorf("campo obrigatório ausente = %v, esperado ErrRequiredField", err)
	}
}

func TestCreateRecord_DuplicateValueRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedUniqueTable(t, db, tenant)

	if _, err := runCreate(t, db, tenant, identity.RoleAdmin, "authors", map[string]any{"email": "ada@example.com"}, nil); err != nil {
		t.Fatalf("primeira criação: %v", err)
	}
	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "authors", map[string]any{"email": "ada@example.com"}, nil)
	if !errors.Is(err, ErrDuplicateValue) {
		t.Errorf("valor duplicado = %v, esperado ErrDuplicateValue", err)
	}
}

func TestCreateRecord_InvalidReferenceRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "A", "pages": 1, "publisher": 999999}, nil)
	if !errors.Is(err, ErrInvalidReference) {
		t.Errorf("FK inválida = %v, esperado ErrInvalidReference", err)
	}
}

func TestCreateRecord_NotAuthorized(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	ctx := context.Background()

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "secrets", metadata.TableOptions{MinRoleWrite: identity.RoleAdmin})
		return err
	}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := runCreate(t, db, tenant, identity.RolePublic, "secrets", map[string]any{}, nil)
	if !errors.Is(err, ErrNotAuthorized) {
		t.Errorf("ator não autorizado = %v, esperado ErrNotAuthorized", err)
	}

	rows, err := runQuery(t, db, tenant, identity.RoleAdmin, Query{Table: "secrets"})
	if err != nil {
		t.Fatalf("verificação: %v", err)
	}
	if len(rows) != 0 {
		t.Error("registro foi criado apesar da autorização ter sido negada")
	}
}

// TestCreateRecord_HookFailureRollsBackEverything cobre "falha desfaz toda
// a operação" pelo ângulo dos pontos de extensão de trigger: um
// AfterInsert que falha desfaz até o INSERT que já tinha rodado dentro da
// mesma transação.
func TestCreateRecord_HookFailureRollsBackEverything(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	boom := errors.New("hook proposital de teste")
	hooks := &Hooks{
		AfterInsert: func(ctx context.Context, tx pgx.Tx, table metadata.Table, record map[string]any) error {
			return boom
		},
	}
	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "Should Not Persist", "pages": 1}, hooks)
	if !errors.Is(err, boom) {
		t.Fatalf("CreateRecord com hook falhando = %v, esperado o erro do hook", err)
	}

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "author", Value: "Should Not Persist"}})
	if err != nil {
		t.Fatalf("verificação: %v", err)
	}
	if len(rows) != 0 {
		t.Error("registro persistiu apesar do hook AfterInsert ter falhado — rollback não aconteceu")
	}
}

func TestCreateRecord_HooksCalledWithCorrectArgs(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	var beforeValues, afterRecord map[string]any
	hooks := &Hooks{
		BeforeInsert: func(ctx context.Context, tx pgx.Tx, table metadata.Table, values map[string]any) error {
			beforeValues = values
			return nil
		},
		AfterInsert: func(ctx context.Context, tx pgx.Tx, table metadata.Table, record map[string]any) error {
			afterRecord = record
			return nil
		},
	}
	_, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "Hooked", "pages": 5}, hooks)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if beforeValues["author"] != "Hooked" {
		t.Errorf("BeforeInsert values = %v", beforeValues)
	}
	if afterRecord["author"] != "Hooked" || afterRecord["id"] == nil {
		t.Errorf("AfterInsert record = %v", afterRecord)
	}
}

func TestUpdateRecord_SuccessAndVersionChanges(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "Original", "pages": 100}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	version := created["_version"].(string)

	updated, err := runUpdate(t, db, tenant, identity.RoleAdmin, "books", id, version, map[string]any{"author": "Updated"}, nil)
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}
	if updated["author"] != "Updated" {
		t.Errorf("author = %v, esperado Updated", updated["author"])
	}
	if updated["_version"] == version {
		t.Error("_version não mudou após UpdateRecord")
	}
}

func TestUpdateRecord_VersionConflict(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "A", "pages": 1}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	staleVersion := created["_version"].(string)

	if _, err := runUpdate(t, db, tenant, identity.RoleAdmin, "books", id, staleVersion, map[string]any{"pages": 2}, nil); err != nil {
		t.Fatalf("primeira atualização: %v", err)
	}

	_, err = runUpdate(t, db, tenant, identity.RoleAdmin, "books", id, staleVersion, map[string]any{"pages": 3}, nil)
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("segunda atualização com versão obsoleta = %v, esperado ErrVersionConflict", err)
	}
}

func TestUpdateRecord_NotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	_, err := runUpdate(t, db, tenant, identity.RoleAdmin, "books", 999999, "1", map[string]any{"pages": 2}, nil)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("UpdateRecord em id inexistente = %v, esperado ErrRecordNotFound", err)
	}
}

func TestUpdateRecord_NoFieldsRejected(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "A", "pages": 1}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	version := created["_version"].(string)

	_, err = runUpdate(t, db, tenant, identity.RoleAdmin, "books", id, version, map[string]any{}, nil)
	if !errors.Is(err, ErrNoFields) {
		t.Errorf("UpdateRecord sem campos = %v, esperado ErrNoFields", err)
	}
}

// TestUpdateRecord_ConcurrentConflict é a prova direta do critério de
// aceite "conflito concorrente retorna erro definido": duas transações
// Postgres REAIS disputando o mesmo registro — a segunda a chegar (que só
// prossegue depois que a primeira libera o lock de linha ao commitar) vê
// um xmin diferente do esperado e recebe ErrVersionConflict, nunca
// sobrescreve silenciosamente.
func TestUpdateRecord_ConcurrentConflict(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "Contested", "pages": 1}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	version := created["_version"].(string)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := runUpdate(t, db, tenant, identity.RoleAdmin, "books", id, version, map[string]any{"pages": 100 + i}, nil)
			results[i] = err
		}(i)
	}
	wg.Wait()

	successes, conflicts := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrVersionConflict):
			conflicts++
		default:
			t.Errorf("resultado inesperado: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Errorf("successes=%d conflicts=%d, esperado 1 e 1 (escritor único sob concorrência real)", successes, conflicts)
	}
}

func TestDeleteRecord_Success(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "ToDelete", "pages": 1}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	version := created["_version"].(string)

	if err := runDelete(t, db, tenant, identity.RoleAdmin, "books", id, version, nil); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "id", Value: id}})
	if err != nil {
		t.Fatalf("verificação: %v", err)
	}
	if len(rows) != 0 {
		t.Error("registro ainda existe após DeleteRecord")
	}
}

func TestDeleteRecord_VersionConflict(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "A", "pages": 1}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	staleVersion := created["_version"].(string)

	if _, err := runUpdate(t, db, tenant, identity.RoleAdmin, "books", id, staleVersion, map[string]any{"pages": 2}, nil); err != nil {
		t.Fatalf("atualização: %v", err)
	}

	err = runDelete(t, db, tenant, identity.RoleAdmin, "books", id, staleVersion, nil)
	if !errors.Is(err, ErrVersionConflict) {
		t.Errorf("DeleteRecord com versão obsoleta = %v, esperado ErrVersionConflict", err)
	}
}

func TestDeleteRecord_NotFound(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	err := runDelete(t, db, tenant, identity.RoleAdmin, "books", 999999, "1", nil)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("DeleteRecord em id inexistente = %v, esperado ErrRecordNotFound", err)
	}
}

func TestDeleteRecord_HookFailureRollsBack(t *testing.T) {
	db := testDB(t)
	tenant := testTenant(t, db)
	seedCorpus(t, db, tenant)

	created, err := runCreate(t, db, tenant, identity.RoleAdmin, "books", map[string]any{"author": "Persisted", "pages": 1}, nil)
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	id := int(created["id"].(int32))
	version := created["_version"].(string)

	boom := errors.New("hook proposital de teste")
	hooks := &Hooks{AfterDelete: func(ctx context.Context, tx pgx.Tx, table metadata.Table, id int) error { return boom }}
	err = runDelete(t, db, tenant, identity.RoleAdmin, "books", id, version, hooks)
	if !errors.Is(err, boom) {
		t.Fatalf("DeleteRecord com hook falhando = %v, esperado o erro do hook", err)
	}

	rows, err := runQuery(t, db, tenant, identity.RolePublic, Query{Table: "books", Where: Eq{Field: "id", Value: id}})
	if err != nil {
		t.Fatalf("verificação: %v", err)
	}
	if len(rows) != 1 {
		t.Error("registro foi removido apesar do hook AfterDelete ter falhado — rollback não aconteceu")
	}
}
