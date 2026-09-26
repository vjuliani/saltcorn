package installation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(src, "sub"), 0700))
	must(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("conteudo a"), 0600))
	must(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("conteudo b"), 0600))

	archive := filepath.Join(t.TempDir(), "out.tar.gz")
	must(t, archiveDir(src, archive))

	dst := filepath.Join(t.TempDir(), "restored")
	must(t, unarchiveDir(archive, dst))

	a, err := os.ReadFile(filepath.Join(dst, "a.txt"))
	must(t, err)
	if string(a) != "conteudo a" {
		t.Fatalf("a.txt = %q", a)
	}
	b, err := os.ReadFile(filepath.Join(dst, "sub", "b.txt"))
	must(t, err)
	if string(b) != "conteudo b" {
		t.Fatalf("sub/b.txt = %q", b)
	}
}

func TestEncryptDecryptFile_RoundTrip(t *testing.T) {
	src := filepath.Join(t.TempDir(), "plain.bin")
	must(t, os.WriteFile(src, []byte("segredo de instalação"), 0600))

	enc := filepath.Join(t.TempDir(), "cipher.bin")
	must(t, encryptFile(src, enc, []byte("senha-forte-o-suficiente")))

	dec := filepath.Join(t.TempDir(), "decrypted.bin")
	must(t, decryptFile(enc, dec, []byte("senha-forte-o-suficiente")))

	got, err := os.ReadFile(dec)
	must(t, err)
	if string(got) != "segredo de instalação" {
		t.Fatalf("conteúdo decifrado = %q", got)
	}
}

func TestDecryptFile_WrongPassphraseFailsExplicitly(t *testing.T) {
	src := filepath.Join(t.TempDir(), "plain.bin")
	must(t, os.WriteFile(src, []byte("segredo"), 0600))
	enc := filepath.Join(t.TempDir(), "cipher.bin")
	must(t, encryptFile(src, enc, []byte("senha-correta-1234")))

	dec := filepath.Join(t.TempDir(), "decrypted.bin")
	if decryptFile(enc, dec, []byte("senha-errada-9999")) == nil {
		t.Fatal("esperado erro com senha errada")
	}
	if _, err := os.Stat(dec); !os.IsNotExist(err) {
		t.Fatal("nenhum arquivo de saída deveria existir após falha")
	}
}

func TestDecryptFile_TamperedCiphertextDetected(t *testing.T) {
	src := filepath.Join(t.TempDir(), "plain.bin")
	must(t, os.WriteFile(src, []byte("segredo"), 0600))
	enc := filepath.Join(t.TempDir(), "cipher.bin")
	must(t, encryptFile(src, enc, []byte("senha-correta-1234")))

	data, err := os.ReadFile(enc)
	must(t, err)
	data[len(data)-1] ^= 0xFF // flip the last ciphertext byte
	must(t, os.WriteFile(enc, data, 0600))

	dec := filepath.Join(t.TempDir(), "decrypted.bin")
	if decryptFile(enc, dec, []byte("senha-correta-1234")) == nil {
		t.Fatal("esperado erro com ciphertext adulterado (GCM deveria detectar)")
	}
}

// TestBackupEncrypted_RoundTrip prova o caso de uso completo: instância
// real → backup cifrado (um único arquivo, não um diretório em texto
// claro) → restore a partir dele, preservando dados, EXATAMENTE como
// Backup/Restore em texto claro já provam em installation_test.go.
func TestBackupEncrypted_RoundTrip(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	must(t, os.WriteFile(filepath.Join(root, "files", "note.txt"), []byte("nota real"), 0600))

	dest := filepath.Join(t.TempDir(), "backup.enc")
	passphrase := []byte("outra-senha-bem-forte")
	must(t, BackupEncrypted(ctx, root, c, dest, passphrase))

	if info, err := os.Stat(dest); err != nil || info.IsDir() {
		t.Fatal("backup cifrado deveria ser um único arquivo, não um diretório")
	}

	restoredRoot := t.TempDir()
	rc, err := RestoreEncrypted(ctx, restoredRoot, dest, passphrase, "")
	must(t, err)
	must(t, Check(ctx, restoredRoot, rc))

	data, err := os.ReadFile(filepath.Join(restoredRoot, "files", "note.txt"))
	must(t, err)
	if string(data) != "nota real" {
		t.Fatalf("arquivo restaurado incorreto: %q", data)
	}

	if _, err := RestoreEncrypted(context.Background(), t.TempDir(), dest, []byte("senha-errada"), ""); err == nil {
		t.Fatal("esperado erro ao restaurar com senha errada")
	}
}
