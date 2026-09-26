package installation

import (
	"testing"
	"time"
)

func names(entries []BackupEntry) map[string]bool {
	out := map[string]bool{}
	for _, e := range entries {
		out[e.Name] = true
	}
	return out
}

func TestApplyRetention_EmptyInput(t *testing.T) {
	keep, prune := ApplyRetention(nil, time.Now(), RetentionPolicy{})
	if len(keep) != 0 || len(prune) != 0 {
		t.Fatalf("esperado vazio, obtido keep=%v prune=%v", keep, prune)
	}
}

// TestApplyRetention_AlwaysKeepsMostRecent prova a rede de segurança: com
// toda a política zerada, o backup mais recente nunca é descartado —
// nenhuma configuração restritiva pode apagar o único ponto de
// recuperação disponível.
func TestApplyRetention_AlwaysKeepsMostRecent(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	entries := []BackupEntry{
		{Name: "a", At: now.AddDate(0, 0, -10)},
		{Name: "b", At: now.AddDate(0, 0, -1)},
	}
	keep, prune := ApplyRetention(entries, now, RetentionPolicy{})
	if len(keep) != 1 || keep[0].Name != "b" {
		t.Fatalf("esperado manter só o mais recente (b), obtido keep=%v", keep)
	}
	if len(prune) != 1 || prune[0].Name != "a" {
		t.Fatalf("esperado descartar a, obtido prune=%v", prune)
	}
}

func TestApplyRetention_DailyKeepsOnePerDay(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	entries := []BackupEntry{
		{Name: "day0-early", At: now.Add(-1 * time.Hour)},
		{Name: "day0-late", At: now},
		{Name: "day1", At: now.AddDate(0, 0, -1)},
		{Name: "day2", At: now.AddDate(0, 0, -2)},
		{Name: "day3", At: now.AddDate(0, 0, -3)},
	}
	keep, prune := ApplyRetention(entries, now, RetentionPolicy{KeepDaily: 2})
	keptNames := names(keep)
	// Two most recent distinct calendar days: today (day0-late, the
	// newest within that day) and yesterday (day1).
	if !keptNames["day0-late"] || !keptNames["day1"] {
		t.Fatalf("esperado manter day0-late e day1, obtido %v", keptNames)
	}
	if keptNames["day0-early"] {
		t.Fatal("não deveria manter dois backups do MESMO dia")
	}
	if len(keep) != 2 {
		t.Fatalf("esperado 2 mantidos, obtido %d: %v", len(keep), keep)
	}
	pruneNames := names(prune)
	if !pruneNames["day0-early"] || !pruneNames["day2"] || !pruneNames["day3"] {
		t.Fatalf("descarte incompleto: %v", pruneNames)
	}
}

func TestApplyRetention_GFSAcrossLevels(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	entries := []BackupEntry{
		{Name: "today", At: now},
		{Name: "1-month-ago", At: now.AddDate(0, -1, 0)},
		{Name: "1-year-ago", At: now.AddDate(-1, 0, 0)},
		{Name: "2-years-ago", At: now.AddDate(-2, 0, 0)},
	}
	keep, prune := ApplyRetention(entries, now, RetentionPolicy{KeepDaily: 1, KeepMonthly: 2, KeepYearly: 3})
	keptNames := names(keep)
	for _, want := range []string{"today", "1-month-ago", "1-year-ago", "2-years-ago"} {
		if !keptNames[want] {
			t.Errorf("esperado manter %q (níveis GFS combinados), obtido keep=%v", want, keptNames)
		}
	}
	if len(prune) != 0 {
		t.Fatalf("esperado nada descartado, obtido %v", prune)
	}
}

func TestApplyRetention_ZeroLevelDisabled(t *testing.T) {
	now := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	entries := []BackupEntry{
		{Name: "today", At: now},
		{Name: "last-week", At: now.AddDate(0, 0, -7)},
	}
	// KeepWeekly=0: nada além do "mais recente de todos" é mantido por
	// esse nível — last-week só sobrevive se algum outro nível o cobrir
	// (nenhum aqui), então deve ser descartado.
	keep, prune := ApplyRetention(entries, now, RetentionPolicy{KeepWeekly: 0})
	if len(keep) != 1 || keep[0].Name != "today" {
		t.Fatalf("esperado manter só today, obtido %v", keep)
	}
	if len(prune) != 1 || prune[0].Name != "last-week" {
		t.Fatalf("esperado descartar last-week, obtido %v", prune)
	}
}
