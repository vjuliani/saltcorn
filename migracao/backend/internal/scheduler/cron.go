// Package scheduler implementa o disparo de triggers por TEMPO (o
// subconjunto de `when_trigger` do legado — Weekly/Daily/Hourly/Often/Cron
// — que GO-024 deixou explicitamente fora de `internal/triggers`, ligado a
// comandos de registro, não a relógio).
//
// Divergência deliberada e unificadora do legado (models/scheduler.ts): em
// vez de cinco mecanismos distintos (config global `next_{name}_event`
// para Hourly/Daily/Weekly sem hora fixa; regex `HH:MM`/`Weekday HH:MM` no
// campo `channel` para Daily/Weekly com hora fixa; expressão cron para
// Cron; "roda todo tick" para Often — cada um sem estado de última
// execução, dependendo só de janelas de tick não sobrepostas), este
// pacote usa UM mecanismo único: uma expressão cron de 5 campos por
// trigger + uma coluna `next_run_at` persistida, avançada
// deterministicamente após cada execução — Hourly/Daily/Weekly/Often do
// legado são só atalhos documentados para expressões cron equivalentes
// ("0 * * * *", "0 0 * * *", etc.); "Often" mapeia para granularidade de
// minuto ("* * * * *"), não para a frequência exata do tick do worker — um
// subconjunto prioritário deliberado, documentado, nunca um comportamento
// silenciosamente diferente.
package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidCronExpr é devolvido por ParseCron quando expr não tem
// exatamente 5 campos ou algum campo tem um valor fora do intervalo válido
// — nunca uma tolerância silenciosa (ex.: ignorar um campo mal formado).
var ErrInvalidCronExpr = errors.New("scheduler: expressão cron inválida")

// cronField é um dos 5 campos de uma expressão — "*" (curinga, sempre
// verdadeiro) ou um conjunto de valores aceitos (suporta listas separadas
// por vírgula, ex. "0,15,30,45"; NÃO suporta intervalos "1-5" nem passos
// "*/5" — fora do subconjunto prioritário desta tarefa, ErrInvalidCronExpr
// explícito em vez de interpretar errado).
type cronField struct {
	wildcard bool
	values   map[int]bool
}

func (f cronField) matches(v int) bool {
	if f.wildcard {
		return true
	}
	return f.values[v]
}

func parseCronField(s string, min, max int, normalize func(int) int) (cronField, error) {
	if s == "*" {
		return cronField{wildcard: true}, nil
	}
	values := make(map[int]bool)
	for _, part := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < min || n > max {
			return cronField{}, fmt.Errorf("%w: campo %q fora do intervalo [%d,%d]", ErrInvalidCronExpr, s, min, max)
		}
		if normalize != nil {
			n = normalize(n)
		}
		values[n] = true
	}
	return cronField{values: values}, nil
}

// CronExpr é uma expressão cron de 5 campos já validada — minute hour
// dom month dow, mesma ordem/semântica do cron Unix clássico (e do
// `models/internal/cron.ts` do legado).
type CronExpr struct {
	minute, hour, dom, month, dow cronField
}

// ParseCron valida e compila expr ("minuto hora dia-do-mês mês
// dia-da-semana") — 0 e 7 são ambos domingo em dow (mesma convenção do
// legado), replicada aqui via normalização (7 vira 0).
func ParseCron(expr string) (CronExpr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return CronExpr{}, fmt.Errorf("%w: esperado 5 campos, recebido %d (%q)", ErrInvalidCronExpr, len(fields), expr)
	}
	minute, err := parseCronField(fields[0], 0, 59, nil)
	if err != nil {
		return CronExpr{}, err
	}
	hour, err := parseCronField(fields[1], 0, 23, nil)
	if err != nil {
		return CronExpr{}, err
	}
	dom, err := parseCronField(fields[2], 1, 31, nil)
	if err != nil {
		return CronExpr{}, err
	}
	month, err := parseCronField(fields[3], 1, 12, nil)
	if err != nil {
		return CronExpr{}, err
	}
	dow, err := parseCronField(fields[4], 0, 7, func(n int) int {
		if n == 7 {
			return 0
		}
		return n
	})
	if err != nil {
		return CronExpr{}, err
	}
	return CronExpr{minute: minute, hour: hour, dom: dom, month: month, dow: dow}, nil
}

// Matches reporta se t (interpretado no Location de t) satisfaz a
// expressão — regra Vixie cron para dia-do-mês/dia-da-semana: se os DOIS
// forem restritos (nenhum "*"), o dia bate se QUALQUER um dos dois bater
// (OR, não AND); se um dos dois for "*", só o outro decide.
func (c CronExpr) Matches(t time.Time) bool {
	if !c.minute.matches(t.Minute()) || !c.hour.matches(t.Hour()) || !c.month.matches(int(t.Month())) {
		return false
	}
	domOK := c.dom.matches(t.Day())
	dowOK := c.dow.matches(int(t.Weekday())) // time.Weekday: Sunday=0..Saturday=6, mesma convenção do cron
	switch {
	case c.dom.wildcard && c.dow.wildcard:
		return true
	case c.dom.wildcard:
		return dowOK
	case c.dow.wildcard:
		return domOK
	default:
		return domOK || dowOK
	}
}

// maxLookaheadMinutes limita a busca por NextAfter — 400 dias em minutos é
// mais que suficiente para qualquer expressão cron realista (o caso mais
// raro, um dia-do-mês+mês específico, ocorre no máximo uma vez por ano) e
// evita um laço genuinamente infinito para uma expressão patologicamente
// impossível de satisfazer (ex.: "0 0 31 2 *" — 31 de fevereiro nunca
// existe).
const maxLookaheadMinutes = 400 * 24 * 60

// ErrNoUpcomingOccurrence é devolvido por NextAfter quando nenhuma
// ocorrência é encontrada dentro de maxLookaheadMinutes — nunca um laço
// infinito silencioso para uma expressão impossível de satisfazer.
var ErrNoUpcomingOccurrence = errors.New("scheduler: nenhuma ocorrência da expressão cron dentro do horizonte de busca")

// NextAfter encontra a primeira ocorrência de c estritamente DEPOIS de
// after, no Location de after — nunca after propriamente dito, mesmo que
// after já satisfaça a expressão (semântica "próxima ocorrência", não
// "esta ou a próxima"). Busca minuto a minuto (mesma técnica de
// `cronDueInWindow` do legado, aplicada para achar a PRÓXIMA ocorrência em
// vez de só testar uma janela).
func (c CronExpr) NextAfter(after time.Time) (time.Time, error) {
	candidate := after.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < maxLookaheadMinutes; i++ {
		if c.Matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, ErrNoUpcomingOccurrence
}
