// Testa o comando `cli backup`/`restore`/`backup-schedule` com
// --passphrase-file de ponta a ponta, chamando installationCommand
// diretamente (não o binário) — mesmo padrão de e2eseed_test.go.
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func mustCLI(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstallationCommand_BackupRestoreWithPassphrase(t *testing.T) {
	root := t.TempDir()
	passphraseFile := filepath.Join(t.TempDir(), "passphrase")
	mustCLI(t, os.WriteFile(passphraseFile, []byte("uma-senha-de-cli-bem-forte\n"), 0600))

	mustCLI(t, installationCommand("setup", []string{"--dir", root, "--driver", "sqlite"}))

	backupDest := filepath.Join(t.TempDir(), "backup.enc")
	mustCLI(t, installationCommand("backup", []string{"--dir", root, "--output", backupDest, "--passphrase-file", passphraseFile}))

	if info, err := os.Stat(backupDest); err != nil || info.IsDir() {
		t.Fatal("backup com --passphrase-file deveria produzir um único arquivo cifrado")
	}

	restoredRoot := t.TempDir()
	mustCLI(t, installationCommand("restore", []string{"--dir", restoredRoot, "--backup", backupDest, "--passphrase-file", passphraseFile}))
	mustCLI(t, installationCommand("check", []string{"--dir", restoredRoot}))

	wrongPassphraseFile := filepath.Join(t.TempDir(), "wrong")
	mustCLI(t, os.WriteFile(wrongPassphraseFile, []byte("senha-errada\n"), 0600))
	if err := installationCommand("restore", []string{"--dir", t.TempDir(), "--backup", backupDest, "--passphrase-file", wrongPassphraseFile}); err == nil {
		t.Fatal("esperado erro ao restaurar com senha errada")
	}
}

func TestInstallationCommand_BackupScheduleRequiresOutput(t *testing.T) {
	root := t.TempDir()
	mustCLI(t, installationCommand("setup", []string{"--dir", root, "--driver", "sqlite"}))
	if err := installationCommand("backup-schedule", []string{"--dir", root}); err == nil {
		t.Fatal("esperado erro sem --output")
	}
}
