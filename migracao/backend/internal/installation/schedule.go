// Agendamento de backup (GO-046, CAP-081) — equivalente reduzido do
// agendamento automático de backup-retention.ts do legado: um laço
// (ticker) roda dentro do MESMO processo administrativo de longa duração
// que já existe (Serve/CLI dedicado), nunca um cron externo ou um job de
// internal/scheduler (aquele mecanismo é por-tenant, disparado por
// trigger de APLICAÇÃO — Weekly/Daily/Hourly de _sc_triggers, GO-025; um
// backup de INSTALAÇÃO inteira é uma preocupação de administração, fora
// de qualquer tenant, sem relação com o catálogo de triggers de um
// tenant).
package installation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// ScheduleConfig parametriza RunScheduledBackups — Passphrase vazia
// desliga a criptografia (backup em texto claro, mesmo formato de
// Backup()); DestDir recebe um backup NOVO a cada Interval.
type ScheduleConfig struct {
	Interval   time.Duration
	DestDir    string
	Retention  RetentionPolicy
	Passphrase []byte
}

const backupNameLayout = "20060102T150405.000Z"

var backupNamePattern = regexp.MustCompile(`^backup-(\d{8}T\d{6}\.\d{3}Z)$`)

// backupEntryName inclui milissegundos de propósito — um Interval curto
// (tipicamente em testes) não pode colidir em nomes distintos dentro do
// mesmo segundo de relógio.
func backupEntryName(at time.Time) string {
	return "backup-" + at.UTC().Format(backupNameLayout)
}

// listBackupEntries lê DestDir e reconhece só nomes no formato de
// backupEntryName — qualquer outro arquivo/diretório é ignorado pela
// retenção (nunca apagado por engano por não bater no padrão esperado).
func listBackupEntries(destDir string) ([]BackupEntry, error) {
	items, err := os.ReadDir(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []BackupEntry
	for _, item := range items {
		m := backupNamePattern.FindStringSubmatch(item.Name())
		if m == nil {
			continue
		}
		at, err := time.Parse(backupNameLayout, m[1])
		if err != nil {
			continue
		}
		out = append(out, BackupEntry{Name: item.Name(), At: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// runOneScheduledBackup executa um backup (cifrado se Passphrase estiver
// definida) para um nome novo, timestampado, dentro de DestDir, e então
// aplica Retention — removendo tanto diretórios (backup em texto claro)
// quanto arquivos (backup cifrado) que a política manda descartar.
func runOneScheduledBackup(ctx context.Context, root string, c Config, sched ScheduleConfig, now time.Time) error {
	if err := os.MkdirAll(sched.DestDir, 0700); err != nil {
		return err
	}
	dest := filepath.Join(sched.DestDir, backupEntryName(now))
	if len(sched.Passphrase) > 0 {
		if err := BackupEncrypted(ctx, root, c, dest, sched.Passphrase); err != nil {
			return err
		}
	} else if err := Backup(ctx, root, c, dest); err != nil {
		return err
	}
	entries, err := listBackupEntries(sched.DestDir)
	if err != nil {
		return err
	}
	_, prune := ApplyRetention(entries, now, sched.Retention)
	for _, e := range prune {
		if err := os.RemoveAll(filepath.Join(sched.DestDir, e.Name)); err != nil {
			return fmt.Errorf("installation: remover backup expirado %q: %w", e.Name, err)
		}
	}
	return nil
}

// RunScheduledBackups roda até ctx ser cancelado — chamador (CLI
// `backup-schedule`) segura o Lock de administração pelo tempo de vida
// inteiro do laço, mesma disciplina de Serve(). O PRIMEIRO backup roda
// imediatamente (nunca espera um Interval inteiro para o primeiro ponto
// de recuperação existir), os seguintes a cada Interval.
func RunScheduledBackups(ctx context.Context, root string, c Config, sched ScheduleConfig) error {
	if sched.Interval <= 0 {
		return fmt.Errorf("installation: intervalo de agendamento deve ser positivo")
	}
	// Um backup interrompido a meio por cancelamento de ctx (shutdown do
	// processo administrativo, ex.: SIGTERM em cmd/cli backup-schedule) é
	// encerramento gracioso, nunca uma falha a reportar — ctx.Err() != nil
	// distingue essa causa de um erro real (disco cheio, Postgres fora do
	// ar) que teria acontecido de qualquer forma, cancelamento ou não.
	if err := runOneScheduledBackup(ctx, root, c, sched, time.Now()); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	ticker := time.NewTicker(sched.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if err := runOneScheduledBackup(ctx, root, c, sched, now); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}
