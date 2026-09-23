// Testes deste arquivo exigem Postgres real — pulam (t.Skip) se
// SALTCORN_GO_TEST_DATABASE_URL não estiver definida. Cobrem GO-051: o
// consumidor HTTP de internal/files (GO-026) que faltava — upload
// multipart real, download com Content-Type correto, e a garantia
// "usuário sem acesso não baixa arquivo" através da fronteira HTTP real
// (não só no nível de pacote, já coberto por internal/files/upload_test.go).
package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/files"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

func multipartFileBody(t *testing.T, fieldName, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("escrever conteúdo multipart: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("fechar multipart writer: %v", err)
	}
	return body, w.FormDataContentType()
}

// TestFilesHTTP_UploadThenDownload é a prova ponta a ponta: enviar um
// arquivo real via multipart, e baixá-lo de volta com os MESMOS bytes e
// o Content-Type resolvido pela extensão.
func TestFilesHTTP_UploadThenDownload(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	backend := files.NewLocalBackend(t.TempDir())
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)
	publicToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.publicID), fx.tenant, time.Minute)

	uploadH := buildEditorHandler(t, verifier, fx.guard, filesCapability, uploadFileHandler(fx.tracker, db, backend))
	downloadH := buildEditorHandler(t, verifier, fx.guard, filesCapability, downloadFileHandler(fx.tracker, db, backend))

	body, contentType := multipartFileBody(t, "file", "photo.png", "conteúdo real do arquivo de teste")
	uploadReq := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/files", body)
	uploadReq.SetPathValue("tenant", string(fx.tenant))
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	uploadRec := httptest.NewRecorder()
	uploadH.ServeHTTP(uploadRec, uploadReq)
	if uploadRec.Code != http.StatusCreated {
		t.Fatalf("upload: status = %d, corpo = %s", uploadRec.Code, uploadRec.Body.String())
	}
	var uploaded fileResponse
	if err := json.Unmarshal(uploadRec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decodificar resposta de upload: %v", err)
	}
	if uploaded.Filename != "photo.png" || uploaded.MimeSuper != "image" || uploaded.MimeSub != "png" || uploaded.SizeBytes == 0 {
		t.Fatalf("uploaded = %+v inesperado", uploaded)
	}

	// Download pelo mesmo ator (admin) — bytes e Content-Type reais.
	downloadReq := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/files/"+strconv.Itoa(uploaded.ID), nil)
	downloadReq.SetPathValue("tenant", string(fx.tenant))
	downloadReq.SetPathValue("id", strconv.Itoa(uploaded.ID))
	downloadReq.Header.Set("Authorization", "Bearer "+adminToken)
	downloadRec := httptest.NewRecorder()
	downloadH.ServeHTTP(downloadRec, downloadReq)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("download: status = %d, corpo = %s", downloadRec.Code, downloadRec.Body.String())
	}
	if ct := downloadRec.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, esperado \"image/png\"", ct)
	}
	if got := downloadRec.Body.String(); got != "conteúdo real do arquivo de teste" {
		t.Errorf("corpo do download = %q, esperado o conteúdo real enviado", got)
	}

	// Ator público (papel insuficiente — o arquivo nasce MinRoleRead=admin)
	// nunca baixa: 404, nunca revela existência (ver files.ErrNotAuthorized).
	forbiddenReq := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/files/"+strconv.Itoa(uploaded.ID), nil)
	forbiddenReq.SetPathValue("tenant", string(fx.tenant))
	forbiddenReq.SetPathValue("id", strconv.Itoa(uploaded.ID))
	forbiddenReq.Header.Set("Authorization", "Bearer "+publicToken)
	forbiddenRec := httptest.NewRecorder()
	downloadH.ServeHTTP(forbiddenRec, forbiddenReq)
	if forbiddenRec.Code != http.StatusNotFound {
		t.Fatalf("download (público, sem acesso): status = %d, esperado 404", forbiddenRec.Code)
	}
}

func TestFilesHTTP_DownloadNonexistent_Is404(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	backend := files.NewLocalBackend(t.TempDir())
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)
	downloadH := buildEditorHandler(t, verifier, fx.guard, filesCapability, downloadFileHandler(fx.tracker, db, backend))

	req := httptest.NewRequest(http.MethodGet, "/v1/tenants/"+string(fx.tenant)+"/files/999999", nil)
	req.SetPathValue("tenant", string(fx.tenant))
	req.SetPathValue("id", "999999")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	downloadH.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, esperado 404", rec.Code)
	}
}

func TestFilesHTTP_UploadWithoutFileField_Is400(t *testing.T) {
	db := testDB(t)
	fx := newEditorFixture(t, db)
	verifier, err := tenancy.NewVerifier([]byte(testServiceIdentitySecret))
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	backend := files.NewLocalBackend(t.TempDir())
	adminToken := mintServiceIdentity(t, testServiceIdentitySecret, strconv.Itoa(fx.adminID), fx.tenant, time.Minute)
	uploadH := buildEditorHandler(t, verifier, fx.guard, filesCapability, uploadFileHandler(fx.tracker, db, backend))

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/tenants/"+string(fx.tenant)+"/files", body)
	req.SetPathValue("tenant", string(fx.tenant))
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	uploadH.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, esperado 400", rec.Code)
	}
}
