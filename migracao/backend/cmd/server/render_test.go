// Testes HTTP de GO-020: a rota GET .../views/{id}/render e o bloqueio de
// publicação de layouts incompatíveis via PATCH .../views/{id} — reaproveita
// editorFixture/buildEditorHandler de views_test.go, mesmo pacote.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/views"
)

const compatibleListLayoutJSON = `{"layout":{"besides":[{"header_label":"Título","contents":{"type":"Field","field_name":"title"}},{"contents":{"type":"Field","field_name":"pages"}}]}}`

// setupBooksWithRecords cria a tabela "books" (leitura pública por padrão,
// ver metadata.CreateTable), os campos title/pages, e insere três
// registros diretamente via internal/records — o fixture mínimo para
// provar que renderListHandler devolve dados REAIS da tabela, não um
// resultado fabricado (mesmo espírito de internal/views/render_test.go).
func setupBooksWithRecords(t *testing.T, db *database.DB, tenant tenancy.Tenant) int {
	t.Helper()
	ctx := context.Background()
	var tableID int
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		table, err := metadata.CreateTable(ctx, database.AsTx(tx), identity.RoleAdmin, "books", metadata.TableOptions{})
		if err != nil {
			return err
		}
		tableID = table.ID
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "title", Type: metadata.FieldText, Required: true}); err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, database.AsTx(tx), identity.RoleAdmin, tableID, metadata.FieldDef{Name: "pages", Type: metadata.FieldInteger}); err != nil {
			return err
		}
		for _, book := range []map[string]any{
			{"title": "Dune", "pages": 412},
			{"title": "Foundation", "pages": 255},
			{"title": "Neuromancer", "pages": 271},
		} {
			if _, err := records.CreateRecord(ctx, tx, identity.RoleAdmin, "books", book, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("setup de books/campos/registros: %v", err)
	}
	return tableID
}

func createCompatibleView(t *testing.T, db *database.DB, tenant tenancy.Tenant, tableID int, minRole identity.RoleID) views.View {
	t.Helper()
	var v views.View
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		var configuration map[string]any
		if err := json.Unmarshal([]byte(compatibleListLayoutJSON), &configuration); err != nil {
			return err
		}
		v, err = views.CreateView(ctx, tx, identity.RoleAdmin, "booklist", tableID, "List", configuration, views.ViewOptions{MinRole: minRole})
		return err
	}); err != nil {
		t.Fatalf("criar view compatível: %v", err)
	}
	return v
}

// TestRenderListHandler_PublishedViewRendersRealRows é a prova de ponta a
// ponta de "aplicações fixture renderizam ... com paridade funcional"
// (critério de aceite de GO-020): tabela real, registros reais inseridos
// via internal/records, view List compatível publicada, e o DTO de
// renderização devolve exatamente essas linhas/colunas — para o ator
// admin E para um ator público, provando que a autorização da VIEW
// (identity.CanRead sobre MinRole) e da TABELA (identity.CanRead sobre
// MinRoleRead, reaproveitada de records.Rows/GO-015) operam juntas.
func TestRenderListHandler_PublishedViewRendersRealRows(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)
	view := createCompatibleView(t, db, fx.tenant, tableID, identity.RolePublic)

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderListHandler(fx.tracker, db))
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render?limit=2", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+publicToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("render (público, view publicada): status = %d, corpo = %s", rec.Code, rec.Body.String())
	}

	var resp renderListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v", err)
	}
	if len(resp.Columns) != 2 || resp.Columns[0].FieldName != "title" || resp.Columns[0].HeaderLabel != "Título" {
		t.Fatalf("resp.Columns = %+v, esperado [title/Título pages/pages]", resp.Columns)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("len(resp.Rows) = %d, esperado 2 (limit=2 de 3 registros reais)", len(resp.Rows))
	}
	if resp.NextCursor == nil {
		t.Error("resp.NextCursor = nil, esperado um cursor (há um 3º registro)")
	}
	if resp.Rows[0]["title"] != "Dune" {
		t.Errorf("resp.Rows[0][title] = %v, esperado Dune", resp.Rows[0]["title"])
	}

	// Página seguinte via o cursor devolvido — completa a paridade
	// funcional de paginação.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render?limit=2&cursor="+*resp.NextCursor, nil)
	req2.SetPathValue("tenant", string(fx.tenant))
	req2.SetPathValue("id", strconv.Itoa(view.ID))
	req2.Header.Set("Authorization", "Bearer "+publicToken)
	rec2 := httptest.NewRecorder()
	renderH.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("render (segunda página): status = %d, corpo = %s", rec2.Code, rec2.Body.String())
	}
	var resp2 renderListResponse
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decodificar segunda página: %v", err)
	}
	if len(resp2.Rows) != 1 || resp2.NextCursor != nil {
		t.Fatalf("segunda página = %+v, esperado 1 linha restante e sem próximo cursor", resp2)
	}
}

// TestRenderListHandler_DeniedBeforePublish prova que a view não publicada
// (MinRole admin) nega o ator público no endpoint de renderização — a
// MESMA checagem de GetView (GO-019), reaproveitada por CompileListPlan.
func TestRenderListHandler_DeniedBeforePublish(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)
	view := createCompatibleView(t, db, fx.tenant, tableID, identity.RoleAdmin)

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderListHandler(fx.tracker, db))
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+publicToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("render (público, view não publicada): status = %d, corpo = %s, esperado 403", rec.Code, rec.Body.String())
	}
}

// TestRenderListHandler_UnsupportedViewReturns422 prova que uma view fora
// do subconjunto suportado (aqui: template "Show") nunca chega a um HTML/
// DTO parcial — 422 com o motivo específico, mesmo para o admin.
func TestRenderListHandler_UnsupportedViewReturns422(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", map[string]any{}, views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view Show: %v", err)
	}

	renderH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, renderListHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID)+"/render", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	renderH.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("render (view Show, admin): status = %d, corpo = %s, esperado 422", rec.Code, rec.Body.String())
	}
	var body apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar erro: %v", err)
	}
	if body.Error.Code != "view_unsupported" {
		t.Errorf("body.Error.Code = %q, esperado view_unsupported", body.Error.Code)
	}
}

// TestListViewsHandler_FiltersByRoleAndTable é o nível HTTP de
// views.ListViews (já testado no domínio em commands_test.go) — a página
// "Views" do admin (GO-020) depende desta rota para enumerar o que existe.
func TestListViewsHandler_FiltersByRoleAndTable(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)
	createCompatibleView(t, db, fx.tenant, tableID, identity.RolePublic)
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := views.CreateView(ctx, tx, identity.RoleAdmin, "draft", tableID, "List", map[string]any{}, views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view draft: %v", err)
	}

	listH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, listViewsHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	adminReq := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views", nil)
	adminReq.SetPathValue("tenant", string(fx.tenant))
	adminReq.Header.Set("Authorization", "Bearer "+adminToken)
	adminRec := httptest.NewRecorder()
	listH.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusOK {
		t.Fatalf("listar views (admin): status = %d, corpo = %s", adminRec.Code, adminRec.Body.String())
	}
	var adminList []viewResponse
	if err := json.Unmarshal(adminRec.Body.Bytes(), &adminList); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(adminList) != 2 {
		t.Fatalf("len(adminList) = %d, esperado 2 (admin vê publicada e rascunho)", len(adminList))
	}

	publicReq := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/views", nil)
	publicReq.SetPathValue("tenant", string(fx.tenant))
	publicReq.Header.Set("Authorization", "Bearer "+publicToken)
	publicRec := httptest.NewRecorder()
	listH.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusOK {
		t.Fatalf("listar views (público): status = %d, corpo = %s", publicRec.Code, publicRec.Body.String())
	}
	var publicList []viewResponse
	if err := json.Unmarshal(publicRec.Body.Bytes(), &publicList); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(publicList) != 1 || publicList[0].Name != "booklist" {
		t.Fatalf("publicList = %+v, esperado só [booklist]", publicList)
	}
}

// TestUpdateViewHandler_PublishBlocksIncompatibleLayoutHTTP é o nível HTTP
// do mesmo bloqueio já provado no domínio
// (internal/views.TestUpdateView_PublishBlocksIncompatibleLayout): PATCH
// tentando publicar uma view "Show" (sem layout de lista nenhum) devolve
// 422 view_unsupported, nunca 200 com uma publicação "meio válida".
func TestUpdateViewHandler_PublishBlocksIncompatibleLayoutHTTP(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	tableID := setupBooksWithRecords(t, db, fx.tenant)

	var view views.View
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		view, err = views.CreateView(ctx, tx, identity.RoleAdmin, "showbook", tableID, "Show", map[string]any{}, views.ViewOptions{})
		return err
	}); err != nil {
		t.Fatalf("criar view Show: %v", err)
	}

	updateViewH := buildEditorHandler(t, verifier, fx.guard, viewsCapability, updateViewHandler(fx.tracker, db))
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)

	publishBody := `{"_version":"` + view.Version + `","min_role":` + strconv.Itoa(int(identity.RolePublic)) + `}`
	req := httptest.NewRequest(http.MethodPatch, "/v1/tenants/"+string(fx.tenant)+"/views/"+strconv.Itoa(view.ID), bytes.NewBufferString(publishBody))
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", strconv.Itoa(view.ID))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Idempotency-Key", "publish-incompatible")
	rec := httptest.NewRecorder()
	updateViewH.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("publicar view Show: status = %d, corpo = %s, esperado 422", rec.Code, rec.Body.String())
	}
	var body apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar erro: %v", err)
	}
	if body.Error.Code != "view_unsupported" {
		t.Errorf("body.Error.Code = %q, esperado view_unsupported", body.Error.Code)
	}

	// A view continua admin-only — nunca uma publicação parcial.
	if err := db.WithTenant(context.Background(), fx.tenant, func(ctx context.Context, tx pgx.Tx) error {
		reread, err := views.GetView(ctx, tx, identity.RoleAdmin, view.ID)
		if err != nil {
			return err
		}
		if reread.MinRole != identity.RoleAdmin {
			t.Errorf("view.MinRole = %v após publicação bloqueada, esperado permanecer RoleAdmin", reread.MinRole)
		}
		return nil
	}); err != nil {
		t.Fatalf("reler view: %v", err)
	}
}
