// Corpus de upload/download exigido pelo critério de aceite de GO-026:
// "usuário sem acesso não baixa arquivo; uploads interrompidos são
// limpos" — contra Postgres real.
package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

func intPtr(v int) *int { return &v }

func TestUpload_CreatesFileAndCatalogEntry(t *testing.T) {
	db, tenant, backend := filesFixture(t)
	ctx := context.Background()

	content := []byte("relatorio confidencial")
	var created File
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = Upload(ctx, tx, backend, File{
			Filename:    "relatorio.txt",
			MimeSuper:   "text",
			MimeSub:     "plain",
			MinRoleRead: identity.RoleAdmin,
			UserID:      intPtr(1),
		}, bytes.NewReader(content))
		return err
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("created.ID = 0, esperado um ID atribuído pelo catálogo")
	}
	if created.SizeBytes != int64(len(content)) {
		t.Fatalf("SizeBytes = %d, esperado %d", created.SizeBytes, len(content))
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		r, f, err := Download(ctx, tx, backend, identity.RoleAdmin, intPtr(1), created.ID)
		if err != nil {
			return err
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("conteúdo baixado = %q, esperado %q", got, content)
		}
		if f.Filename != "relatorio.txt" {
			t.Errorf("Filename = %q, esperado \"relatorio.txt\"", f.Filename)
		}
		return nil
	}); err != nil {
		t.Fatalf("Download: %v", err)
	}
}

// TestUpload_CreateFileFails_CleansUpPhysicalFile prova que Upload nunca
// deixa um arquivo físico ÓRFÃO quando o registro de catálogo falha —
// força a falha via uma colisão de storage_key (violação da constraint
// UNIQUE), usando newStorageKeyFunc para controlar a chave gerada.
func TestUpload_CreateFileFails_CleansUpPhysicalFile(t *testing.T) {
	db, tenant, backend := filesFixture(t)
	ctx := context.Background()

	const collidingKey = "chave-fixa-para-forcar-colisao"
	restore := newStorageKeyFunc
	newStorageKeyFunc = func() string { return collidingKey }
	defer func() { newStorageKeyFunc = restore }()

	// Primeiro upload — sucede, ocupa collidingKey.
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Upload(ctx, tx, backend, File{Filename: "primeiro.txt", MimeSuper: "text", MimeSub: "plain", MinRoleRead: identity.RolePublic}, bytes.NewReader([]byte("um")))
		return err
	}); err != nil {
		t.Fatalf("primeiro Upload: %v", err)
	}

	// Segundo upload — MESMA chave (newStorageKeyFunc forçado) — Save
	// grava um NOVO arquivo físico sob a mesma chave final (sobrescrevendo
	// fisicamente, já que LocalBackend não impede isso), mas CreateFile
	// falha por violar a UNIQUE constraint de storage_key.
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Upload(ctx, tx, backend, File{Filename: "segundo.txt", MimeSuper: "text", MimeSub: "plain", MinRoleRead: identity.RolePublic}, bytes.NewReader([]byte("dois")))
		return err
	})
	if err == nil {
		t.Fatal("esperado erro no segundo Upload (storage_key duplicada)")
	}

	// O arquivo físico sob a chave colidida foi removido pela limpeza de
	// Upload — nunca fica um arquivo físico órfão sem catálogo
	// correspondente ao segundo upload que falhou.
	if _, err := backend.Open(ctx, collidingKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open(%q) após CreateFile falhar = %v, esperado ErrNotFound (limpo pelo Upload)", collidingKey, err)
	}
}

// TestDownload_UserWithoutAccess_NeverReadsBytes prova diretamente o
// critério de aceite "usuário sem acesso não baixa arquivo": um ator sem
// papel suficiente e sem ser o dono recebe ErrNotAuthorized ANTES de
// qualquer leitura do backend físico.
func TestDownload_UserWithoutAccess_NeverReadsBytes(t *testing.T) {
	db, tenant, backend := filesFixture(t)
	ctx := context.Background()

	var created File
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = Upload(ctx, tx, backend, File{
			Filename:    "confidencial.txt",
			MimeSuper:   "text",
			MimeSub:     "plain",
			MinRoleRead: identity.RoleAdmin,
			UserID:      intPtr(1),
		}, bytes.NewReader([]byte("segredo")))
		return err
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := Download(ctx, tx, backend, identity.RolePublic, intPtr(2), created.ID)
		return err
	})
	if !errors.Is(err, ErrNotAuthorized) {
		t.Fatalf("err = %v, esperado ErrNotAuthorized", err)
	}
}

// TestDownload_Owner_CanReadRegardlessOfRole prova o outro lado da mesma
// regra: o DONO do arquivo sempre pode baixar, mesmo com papel público —
// mesma regra do legado (role <= min_role_read || user_id === file.user_id).
func TestDownload_Owner_CanReadRegardlessOfRole(t *testing.T) {
	db, tenant, backend := filesFixture(t)
	ctx := context.Background()

	var created File
	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = Upload(ctx, tx, backend, File{
			Filename:    "meu-arquivo.txt",
			MimeSuper:   "text",
			MimeSub:     "plain",
			MinRoleRead: identity.RoleAdmin,
			UserID:      intPtr(7),
		}, bytes.NewReader([]byte("meu conteudo")))
		return err
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		r, _, err := Download(ctx, tx, backend, identity.RolePublic, intPtr(7), created.ID)
		if err != nil {
			return err
		}
		defer r.Close()
		return nil
	}); err != nil {
		t.Fatalf("Download (dono, papel público): %v, esperado sucesso", err)
	}
}

func TestDownload_UnknownFile_ReturnsNotFound(t *testing.T) {
	db, tenant, backend := filesFixture(t)
	ctx := context.Background()

	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, _, err := Download(ctx, tx, backend, identity.RoleAdmin, nil, 99999)
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, esperado ErrNotFound", err)
	}
}

func TestUpload_InterruptedUpload_NoCatalogEntryCreated(t *testing.T) {
	db, tenant, backend := filesFixture(t)
	ctx := context.Background()

	reader := &errAfterNReader{payload: []byte("0123456789"), n: 3}
	err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		_, err := Upload(ctx, tx, backend, File{Filename: "interrompido.txt", MimeSuper: "text", MimeSub: "plain", MinRoleRead: identity.RolePublic}, reader)
		return err
	})
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, esperado envolver errInterrupted", err)
	}

	if err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if scanErr := tx.QueryRow(ctx, `SELECT count(*) FROM _sc_files`).Scan(&count); scanErr != nil {
			return scanErr
		}
		if count != 0 {
			t.Errorf("_sc_files tem %d linha(s), esperado 0 — upload interrompido nunca deveria ter chegado a CreateFile", count)
		}
		return nil
	}); err != nil {
		t.Fatalf("verificação: %v", err)
	}

	entries, err := os.ReadDir(backend.RootDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("diretório de armazenamento tem %d entrada(s), esperado 0", len(entries))
	}
}
