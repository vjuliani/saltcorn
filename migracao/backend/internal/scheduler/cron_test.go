// Corpus de expressões cron — réplica dos padrões representativos de
// packages/saltcorn-data/tests/actions.test.ts ("Cron expressions"): campos
// válidos/inválidos, regra Vixie OR entre dia-do-mês/dia-da-semana, 0 e 7
// ambos domingo, e NextAfter (que o legado não tem — lá é só "due dentro
// da janela", aqui é "próxima ocorrência", um mecanismo mais forte).
package scheduler

import (
	"errors"
	"testing"
	"time"
)

func TestParseCron_Valid(t *testing.T) {
	cases := []string{
		"* * * * *",
		"0 0 * * *",
		"30 9 * * 1",
		"0,15,30,45 * * * *",
		"0 0 1 1 *",
		"0 0 * * 0",
		"0 0 * * 7", // domingo, forma alternativa
	}
	for _, expr := range cases {
		if _, err := ParseCron(expr); err != nil {
			t.Errorf("ParseCron(%q) erro inesperado: %v", expr, err)
		}
	}
}

func TestParseCron_Invalid(t *testing.T) {
	cases := []string{
		"",
		"* * * *",     // só 4 campos
		"60 * * * *",  // minuto fora do intervalo
		"* 24 * * *",  // hora fora do intervalo
		"* * 32 * *",  // dia-do-mês fora do intervalo
		"* * * 13 *",  // mês fora do intervalo
		"* * * * 8",   // dia-da-semana fora do intervalo
		"* * * * abc", // não numérico
		"1-5 * * * *", // intervalo — fora do subconjunto prioritário
		"*/5 * * * *", // passo — fora do subconjunto prioritário
	}
	for _, expr := range cases {
		if _, err := ParseCron(expr); !errors.Is(err, ErrInvalidCronExpr) {
			t.Errorf("ParseCron(%q) err = %v, esperado ErrInvalidCronExpr", expr, err)
		}
	}
}

func TestCronExpr_Matches_DomDowOrRule(t *testing.T) {
	// "0 0 1 * 1" — meia-noite do dia 1 do mês OU toda segunda-feira
	// (regra Vixie: os dois campos são restritos, então OR).
	expr, err := ParseCron("0 0 1 * 1")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}

	day1NotMonday := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC) // domingo
	if !expr.Matches(day1NotMonday) {
		t.Error("dia 1 do mês (não segunda) deveria bater via OR do dia-do-mês")
	}

	mondayNotDay1 := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // segunda-feira, dia 2
	if !expr.Matches(mondayNotDay1) {
		t.Error("segunda-feira (não dia 1) deveria bater via OR do dia-da-semana")
	}

	neitherDay1NorMonday := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC) // terça, dia 3
	if expr.Matches(neitherDay1NorMonday) {
		t.Error("nem dia 1 nem segunda-feira não deveria bater")
	}
}

func TestCronExpr_Matches_WildcardDayFieldsAndBothZeroAndSevenMeanSunday(t *testing.T) {
	viaZero, err := ParseCron("0 0 * * 0")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	viaSeven, err := ParseCron("0 0 * * 7")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	sunday := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if time.Sunday != sunday.Weekday() {
		t.Fatal("premissa do teste inválida: 2026-03-01 deveria ser domingo")
	}
	if !viaZero.Matches(sunday) || !viaSeven.Matches(sunday) {
		t.Error("tanto dow=0 quanto dow=7 deveriam bater num domingo")
	}
	monday := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	if viaZero.Matches(monday) || viaSeven.Matches(monday) {
		t.Error("dow=0/7 não deveriam bater numa segunda-feira")
	}
}

func TestCronExpr_NextAfter_NeverReturnsTheSameInstant(t *testing.T) {
	expr, err := ParseCron("* * * * *") // todo minuto
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	next, err := expr.NextAfter(now)
	if err != nil {
		t.Fatalf("NextAfter: %v", err)
	}
	want := time.Date(2026, 3, 1, 12, 1, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextAfter(%v) = %v, esperado %v (nunca o mesmo instante, mesmo casando)", now, next, want)
	}
}

func TestCronExpr_NextAfter_DailyAtFixedTime(t *testing.T) {
	expr, err := ParseCron("30 9 * * *") // todo dia às 09:30
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	// Antes das 09:30 do mesmo dia — próxima ocorrência é hoje.
	before := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	next, err := expr.NextAfter(before)
	if err != nil {
		t.Fatalf("NextAfter: %v", err)
	}
	wantToday := time.Date(2026, 3, 1, 9, 30, 0, 0, time.UTC)
	if !next.Equal(wantToday) {
		t.Errorf("NextAfter(%v) = %v, esperado %v", before, next, wantToday)
	}

	// Depois das 09:30 — próxima ocorrência é amanhã.
	after := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	next2, err := expr.NextAfter(after)
	if err != nil {
		t.Fatalf("NextAfter: %v", err)
	}
	wantTomorrow := time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)
	if !next2.Equal(wantTomorrow) {
		t.Errorf("NextAfter(%v) = %v, esperado %v", after, next2, wantTomorrow)
	}
}

// TestCronExpr_NextAfter_RespectsLocation prova o item "timezone" do
// escopo de GO-025: a MESMA expressão ("09:00 todo dia"), avaliada contra
// o MESMO instante de referência mas em Locations diferentes, produz
// instantes absolutos (UTC) diferentes — nunca a ambiguidade do legado
// (hora local do servidor, sem fuso explícito por trigger,
// models/internal/cron.ts comentário "evaluated in the server's local
// timezone"). Usa um instante de referência FIXO (não time.Now()) para
// que o resultado seja determinístico independente de quando o teste
// rodar — perto da troca de dia, "now" relativo produziria uma diferença
// que não é sempre 3h exatas (o próximo 09:00 de cada fuso pode cair em
// dias de calendário diferentes dependendo da hora atual).
func TestCronExpr_NextAfter_RespectsLocation(t *testing.T) {
	expr, err := ParseCron("0 9 * * *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	saoPaulo, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Skipf("banco de fusos horários sem America/Sao_Paulo: %v", err)
	}

	reference := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

	nextUTC, err := expr.NextAfter(reference.In(time.UTC))
	if err != nil {
		t.Fatalf("NextAfter (UTC): %v", err)
	}
	wantUTC := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	if !nextUTC.Equal(wantUTC) {
		t.Fatalf("NextAfter (UTC) = %v, esperado %v", nextUTC, wantUTC)
	}

	nextSP, err := expr.NextAfter(reference.In(saoPaulo))
	if err != nil {
		t.Fatalf("NextAfter (America/Sao_Paulo): %v", err)
	}
	// America/Sao_Paulo é UTC-3 (sem horário de verão desde 2019): 09:00
	// local em 2026-03-01 é 12:00 UTC do mesmo dia.
	wantSP := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if !nextSP.UTC().Equal(wantSP) {
		t.Fatalf("NextAfter (America/Sao_Paulo) em UTC = %v, esperado %v", nextSP.UTC(), wantSP)
	}

	diff := nextSP.Sub(nextUTC)
	if diff != 3*time.Hour {
		t.Errorf("diferença = %v, esperado 3h — o MESMO horário de parede (09:00) em fusos diferentes é um instante absoluto diferente", diff)
	}
}

func TestCronExpr_NextAfter_NoUpcomingOccurrence(t *testing.T) {
	// 31 de fevereiro nunca existe — expressão válida sintaticamente, mas
	// nunca satisfazível.
	expr, err := ParseCron("0 0 31 2 *")
	if err != nil {
		t.Fatalf("ParseCron: %v", err)
	}
	_, err = expr.NextAfter(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ErrNoUpcomingOccurrence) {
		t.Fatalf("err = %v, esperado ErrNoUpcomingOccurrence", err)
	}
}
