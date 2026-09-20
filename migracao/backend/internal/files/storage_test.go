// Corpus de armazenamento local exigido pelo critério de aceite de
// GO-026: "uploads interrompidos são limpos" — sem Postgres, só
// filesystem real num diretório temporário descartável por teste.
package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// errAfterNReader devolve n bytes de payload e então um erro — simula um
// upload interrompido (cliente desconectou, corpo da requisição HTTP
// falhou no meio) de forma determinística e testável, sem depender de
// matar uma conexão de verdade.
type errAfterNReader struct {
	payload []byte
	n       int
	sent    int
}

func (r *errAfterNReader) Read(p []byte) (int, error) {
	if r.sent >= r.n {
		return 0, errInterrupted
	}
	remaining := r.n - r.sent
	if remaining > len(p) {
		remaining = len(p)
	}
	if remaining > len(r.payload)-r.sent {
		remaining = len(r.payload) - r.sent
	}
	copy(p, r.payload[r.sent:r.sent+remaining])
	r.sent += remaining
	return remaining, nil
}

var errInterrupted = errors.New("conexão simulada interrompida")

func TestLocalBackend_SaveAndOpen(t *testing.T) {
	backend := NewLocalBackend(t.TempDir())
	ctx := context.Background()

	content := []byte("conteudo de teste")
	size, err := backend.Save(ctx, "arquivo1", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if size != int64(len(content)) {
		t.Fatalf("size = %d, esperado %d", size, len(content))
	}

	r, err := backend.Open(ctx, "arquivo1")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ler conteúdo: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("conteúdo = %q, esperado %q", got, content)
	}
}

func TestLocalBackend_Open_NotFound(t *testing.T) {
	backend := NewLocalBackend(t.TempDir())
	_, err := backend.Open(context.Background(), "nao-existe")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, esperado ErrNotFound", err)
	}
}

func TestLocalBackend_Delete_IdempotentForMissingKey(t *testing.T) {
	backend := NewLocalBackend(t.TempDir())
	if err := backend.Delete(context.Background(), "nunca-existiu"); err != nil {
		t.Fatalf("Delete de chave inexistente: %v, esperado nil (idempotente)", err)
	}
}

// TestLocalBackend_InterruptedUpload_CleansUpStagingImmediately prova o
// critério de aceite "uploads interrompidos são limpos": um upload que
// falha no meio (leitura interrompida) não deixa NENHUM arquivo de
// staging para trás — a limpeza é síncrona, dentro da própria chamada de
// Save, não depende de uma varredura posterior.
func TestLocalBackend_InterruptedUpload_CleansUpStagingImmediately(t *testing.T) {
	dir := t.TempDir()
	backend := NewLocalBackend(dir)

	reader := &errAfterNReader{payload: []byte("0123456789"), n: 5}
	_, err := backend.Save(context.Background(), "upload-interrompido", reader)
	if !errors.Is(err, errInterrupted) {
		t.Fatalf("err = %v, esperado envolver errInterrupted", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("diretório de armazenamento tem %d entrada(s) após upload interrompido, esperado 0: %v", len(entries), names)
	}

	// O arquivo final NUNCA chegou a existir.
	if _, err := backend.Open(context.Background(), "upload-interrompido"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Open após upload interrompido = %v, esperado ErrNotFound", err)
	}
}

// TestLocalBackend_CleanupOrphans_RemovesOldStagingKeepsFresh prova a
// rede de segurança contra uma queda ABRUPTA do processo (que nenhum
// defer de Save intercepta): um arquivo de staging ANTIGO é removido, um
// FRESCO (simulando um upload real ainda em andamento) nunca é tocado.
func TestLocalBackend_CleanupOrphans_RemovesOldStagingKeepsFresh(t *testing.T) {
	dir := t.TempDir()
	backend := NewLocalBackend(dir)

	oldStaging := filepath.Join(dir, "arquivo"+uploadingSuffixPrefix+"antigo")
	if err := os.WriteFile(oldStaging, []byte("orfao"), 0o644); err != nil {
		t.Fatalf("criar staging antigo: %v", err)
	}
	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(oldStaging, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	freshStaging := filepath.Join(dir, "arquivo"+uploadingSuffixPrefix+"fresco")
	if err := os.WriteFile(freshStaging, []byte("em-andamento"), 0o644); err != nil {
		t.Fatalf("criar staging fresco: %v", err)
	}

	removed, err := backend.CleanupOrphans(10 * time.Minute)
	if err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, esperado 1", removed)
	}
	if _, err := os.Stat(oldStaging); !errors.Is(err, os.ErrNotExist) {
		t.Error("staging antigo ainda existe, esperado removido")
	}
	if _, err := os.Stat(freshStaging); err != nil {
		t.Errorf("staging fresco foi removido/alterado, esperado intacto: %v", err)
	}
}

func TestLocalBackend_CleanupOrphans_EmptyDirIsNoop(t *testing.T) {
	backend := NewLocalBackend(filepath.Join(t.TempDir(), "nao-criado-ainda"))
	removed, err := backend.CleanupOrphans(time.Minute)
	if err != nil {
		t.Fatalf("CleanupOrphans em diretório inexistente: %v, esperado nil", err)
	}
	if removed != 0 {
		t.Fatalf("removed = %d, esperado 0", removed)
	}
}

func TestNewStorageKey_NeverEmptyNeverRepeatsObviously(t *testing.T) {
	a := NewStorageKey()
	b := NewStorageKey()
	if a == "" || b == "" {
		t.Fatal("NewStorageKey() não deveria devolver string vazia")
	}
	if a == b {
		t.Fatal("duas chamadas consecutivas devolveram a MESMA chave — aleatoriedade insuficiente")
	}
	if strings.Contains(a, "/") || strings.Contains(a, "..") {
		t.Fatalf("NewStorageKey() = %q contém caracteres perigosos para um caminho de arquivo", a)
	}
}

// An unavailable mount/path must fail without publishing a partial object;
// restoring the path must allow a retry with the same storage key.
func TestLocalBackend_UnavailablePathRecovers(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mount")
	if err := os.WriteFile(root, []byte("unavailable mount"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := NewLocalBackend(root)
	if _, err := backend.Save(context.Background(), "retry-key", strings.NewReader("payload")); err == nil {
		t.Fatal("expected filesystem failure")
	}
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Save(context.Background(), "retry-key", strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	r, err := backend.Open(context.Background(), "retry-key")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "payload" {
		t.Fatalf("retry content %q: %v", data, err)
	}
}
