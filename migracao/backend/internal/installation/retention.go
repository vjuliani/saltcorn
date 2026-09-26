// Retenção GFS (GO-046, CAP-081) — equivalente reduzido de
// backup-retention.ts do legado: mantém um número configurável de
// backups diários/semanais/mensais/anuais mais recentes, descarta o
// resto. Função pura (sem I/O) — quem chama decide o que fazer com a
// lista de pruning (backup.go remove os arquivos/diretórios reais).
package installation

import (
	"fmt"
	"sort"
	"time"
)

// RetentionPolicy é o número de pontos a manter em cada nível — zero
// desativa esse nível (nenhum backup é mantido só por ser "o mais recente
// da semana", por exemplo).
type RetentionPolicy struct {
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
}

// BackupEntry identifica um backup existente pelo horário em que foi
// criado — Name é opaco para esta função, só ecoado de volta em keep/prune.
type BackupEntry struct {
	Name string
	At   time.Time
}

// ApplyRetention decide quais entries mantém e quais descarta, segundo
// policy, tomando now como referência. O backup mais recente de TODOS é
// sempre mantido, mesmo com toda a política zerada — nunca se apaga o
// único ponto de recuperação disponível por causa de uma política restrita
// ou mal configurada.
func ApplyRetention(entries []BackupEntry, now time.Time, policy RetentionPolicy) (keep, prune []BackupEntry) {
	if len(entries) == 0 {
		return nil, nil
	}
	sorted := append([]BackupEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At.After(sorted[j].At) })

	kept := map[string]bool{sorted[0].Name: true}

	keepBucketed := func(bucketOf func(time.Time) string, n int) {
		seen := map[string]bool{}
		for _, e := range sorted {
			if len(seen) >= n {
				return
			}
			b := bucketOf(e.At)
			if !seen[b] {
				seen[b] = true
				kept[e.Name] = true
			}
		}
	}

	dayBucket := func(t time.Time) string { return t.UTC().Format("2006-01-02") }
	weekBucket := func(t time.Time) string { y, w := t.UTC().ISOWeek(); return fmt.Sprintf("%d-W%02d", y, w) }
	monthBucket := func(t time.Time) string { return t.UTC().Format("2006-01") }
	yearBucket := func(t time.Time) string { return t.UTC().Format("2006") }

	if policy.KeepDaily > 0 {
		keepBucketed(dayBucket, policy.KeepDaily)
	}
	if policy.KeepWeekly > 0 {
		keepBucketed(weekBucket, policy.KeepWeekly)
	}
	if policy.KeepMonthly > 0 {
		keepBucketed(monthBucket, policy.KeepMonthly)
	}
	if policy.KeepYearly > 0 {
		keepBucketed(yearBucket, policy.KeepYearly)
	}

	for _, e := range sorted {
		if kept[e.Name] {
			keep = append(keep, e)
		} else {
			prune = append(prune, e)
		}
	}
	return keep, prune
}
