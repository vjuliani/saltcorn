package installation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRunScheduledBackups_RunsImmediatelyAndOnEachTick prova que o
// PRIMEIRO backup roda sem esperar um Interval inteiro, e que backups
// NOVOS continuam sendo criados nos ticks seguintes, até o contexto ser
// cancelado — a retenção padrão (política zerada) mantém só o mais
// recente a cada rodada (ver TestApplyRetention_AlwaysKeepsMostRecent),
// então o sinal observável de "o laço está vivo" é o NOME do backup mais
// recente mudando repetidas vezes, não a contagem acumulada em disco
// (essa é TestRunScheduledBackups_AppliesRetention, com uma política
// explícita).
func TestRunScheduledBackups_RunsImmediatelyAndOnEachTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root, c := newConfig(t, "sqlite")
	destDir := t.TempDir()

	done := make(chan error, 1)
	go func() {
		done <- RunScheduledBackups(ctx, root, c, ScheduleConfig{Interval: 20 * time.Millisecond, DestDir: destDir})
	}()

	seen := map[string]bool{}
	deadline := time.Now().Add(2 * time.Second)
	for len(seen) < 3 {
		select {
		case err := <-done:
			t.Fatalf("RunScheduledBackups encerrou cedo demais: %v", err)
		default:
		}
		entries, err := listBackupEntries(destDir)
		must(t, err)
		if len(entries) > 0 {
			seen[entries[len(entries)-1].Name] = true
		}
		if time.Now().After(deadline) {
			t.Fatalf("esperado ao menos 3 backups distintos ao longo do tempo, obtido %d: %v", len(seen), seen)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunScheduledBackups: %v", err)
	}

	// Depois do cancelamento, nenhum backup novo deve aparecer.
	afterCancel, err := listBackupEntries(destDir)
	must(t, err)
	time.Sleep(100 * time.Millisecond)
	afterGrace, err := listBackupEntries(destDir)
	must(t, err)
	if len(afterCancel) != len(afterGrace) || (len(afterGrace) > 0 && afterCancel[len(afterCancel)-1].Name != afterGrace[len(afterGrace)-1].Name) {
		t.Fatal("backup criado após cancelamento do contexto")
	}
}

// TestRunScheduledBackups_AppliesRetention prova o caso real de ponta a
// ponta: uma política restritiva (KeepDaily:1) mantém só o backup mais
// recente, mesmo com vários já acumulados no diretório.
func TestRunScheduledBackups_AppliesRetention(t *testing.T) {
	ctx := context.Background()
	root, c := newConfig(t, "sqlite")
	destDir := t.TempDir()

	// Backups "antigos" sintéticos, com nomes/timestamps no passado —
	// mais rápido e determinístico do que esperar o relógio real avançar
	// dias inteiros dentro de um teste.
	past := time.Now().Add(-72 * time.Hour)
	for i := 0; i < 3; i++ {
		at := past.Add(time.Duration(i) * 24 * time.Hour)
		must(t, Backup(ctx, root, c, filepath.Join(destDir, backupEntryName(at))))
	}
	entries, err := listBackupEntries(destDir)
	must(t, err)
	if len(entries) != 3 {
		t.Fatalf("esperado 3 backups sintéticos, obtido %d", len(entries))
	}

	must(t, runOneScheduledBackup(ctx, root, c, ScheduleConfig{DestDir: destDir, Retention: RetentionPolicy{KeepDaily: 1}}, time.Now()))

	remaining, err := listBackupEntries(destDir)
	must(t, err)
	if len(remaining) != 1 {
		t.Fatalf("esperado 1 backup restante após retenção, obtido %d: %v", len(remaining), remaining)
	}
	if remaining[0].At.Before(past.Add(48 * time.Hour)) {
		t.Fatalf("retenção manteve o backup errado: %v", remaining[0])
	}
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(destDir, e.Name)); err == nil && e.Name != remaining[0].Name {
			t.Fatalf("backup expirado %q não foi removido do disco", e.Name)
		}
	}
}

// TestRunScheduledBackups_CancelDuringBackupIsGraceful prova um achado
// real: cancelar ctx enquanto um backup está EM CURSO não deve propagar
// como erro — Backup()/BackupEncrypted() usam ctx internamente (I/O de
// banco), e um cancelamento a meio caminho os faz retornar "context
// canceled"; RunScheduledBackups deve reconhecer essa causa (via
// ctx.Err() != nil) e encerrar como desligamento gracioso (nil), nunca
// como falha — o mesmo desligamento por SIGTERM que `cmd/cli
// backup-schedule` usa na prática.
func TestRunScheduledBackups_CancelDuringBackupIsGraceful(t *testing.T) {
	root, c := newConfig(t, "sqlite")
	destDir := t.TempDir()
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(time.Millisecond)
			cancel()
		}()
		err := RunScheduledBackups(ctx, root, c, ScheduleConfig{Interval: time.Hour, DestDir: destDir})
		if err != nil {
			t.Fatalf("cancelamento durante backup deveria ser gracioso (nil), obtido: %v", err)
		}
	}
}

func TestRunScheduledBackups_RejectsNonPositiveInterval(t *testing.T) {
	root, c := newConfig(t, "sqlite")
	if RunScheduledBackups(context.Background(), root, c, ScheduleConfig{Interval: 0, DestDir: t.TempDir()}) == nil {
		t.Fatal("esperado erro para Interval não positivo")
	}
}

// TestListBackupEntries_IgnoresUnrecognizedNames prova que a retenção
// nunca considera (e portanto nunca apaga) um arquivo/diretório que não
// bata no formato esperado de backupEntryName.
func TestListBackupEntries_IgnoresUnrecognizedNames(t *testing.T) {
	destDir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(destDir, "readme.txt"), []byte("not a backup"), 0600))
	must(t, os.Mkdir(filepath.Join(destDir, "not-a-backup-dir"), 0700))
	entries, err := listBackupEntries(destDir)
	must(t, err)
	if len(entries) != 0 {
		t.Fatalf("esperado 0 entradas reconhecidas, obtido %v", entries)
	}
}
