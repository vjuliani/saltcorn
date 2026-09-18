// Corpus de coerção exigido pelo critério de aceite de GO-023: "corpus
// cobre coerção, null, datas, decimal, erros e async" — a parte "async" é
// coberta em internal/expression (que depende deste pacote), o resto é
// coberto aqui, por tipo.
package types

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
)

func TestCoerce_NullIsUniversal(t *testing.T) {
	for _, ft := range []metadata.FieldType{metadata.FieldText, metadata.FieldInteger, metadata.FieldBoolean, metadata.FieldFloat, metadata.FieldDate} {
		got, err := Coerce(ft, nil)
		if err != nil || got != nil {
			t.Errorf("Coerce(%s, nil) = (%v, %v), esperado (nil, nil)", ft, got, err)
		}
	}
}

func TestCoerce_UnsupportedFieldType(t *testing.T) {
	if _, err := Coerce(metadata.FieldKey, 1); !errors.Is(err, metadata.ErrUnsupportedFieldType) {
		t.Fatalf("Coerce(FieldKey, ...) err = %v, esperado ErrUnsupportedFieldType", err)
	}
}

func TestCoerceText(t *testing.T) {
	cases := []struct {
		name    string
		raw     any
		want    string
		wantErr error
	}{
		{"string simples", "olá", "olá", nil},
		{"remove byte nulo", "a\x00b", "ab", nil},
		{"número não é coagido — erro explícito", 42, "", ErrCoercionInvalid},
		{"bool não é coagido — erro explícito", true, "", ErrCoercionInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CoerceText(c.raw)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, esperado %v", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("CoerceText(%v) = (%q, %v), esperado (%q, nil)", c.raw, got, err, c.want)
			}
		})
	}
}

func TestCoerceInteger(t *testing.T) {
	cases := []struct {
		name    string
		raw     any
		want    int64
		wantErr error
	}{
		{"int direto", 42, 42, nil},
		{"float64 arredonda para cima", 2.6, 3, nil},
		{"float64 arredonda para baixo", 2.4, 2, nil},
		{"negativo", -7, -7, nil},
		{"zero", 0, 0, nil},
		{"string numérica simples", "42", 42, nil},
		{"string numérica com espaços (equivalente a +v do JS)", "  42  ", 42, nil},
		{"string com notação científica", "1e2", 100, nil},
		{"string não numérica — erro explícito, nunca NaN silencioso", "abc", 0, ErrCoercionInvalid},
		{"string mista número+texto — erro explícito", "42kg", 0, ErrCoercionInvalid},
		{"bool não suportado — erro explícito", true, 0, ErrCoercionInvalid},
		{"NaN — erro explícito", math.NaN(), 0, ErrCoercionInvalid},
		{"+Inf — erro explícito", math.Inf(1), 0, ErrCoercionInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CoerceInteger(c.raw)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, esperado %v", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("CoerceInteger(%v) = (%d, %v), esperado (%d, nil)", c.raw, got, err, c.want)
			}
		})
	}
}

func TestCoerceFloat_DecimalCorpus(t *testing.T) {
	cases := []struct {
		name    string
		raw     any
		want    float64
		wantErr error
	}{
		{"float64 direto", 19.9, 19.9, nil},
		{"int coagido para float", 3, 3.0, nil},
		{"string numérica estrita", "19.9", 19.9, nil},
		{"string com espaços", "  3.14  ", 3.14, nil},
		{"string suja (moeda) — divergência deliberada do legado, erro explícito", "R$ 10,50", 0, ErrCoercionUnsupportedFormat},
		{"NaN — erro explícito", math.NaN(), 0, ErrCoercionInvalid},
		{"tipo não numérico — erro explícito", true, 0, ErrCoercionInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CoerceFloat(c.raw)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, esperado %v", err, c.wantErr)
				}
				return
			}
			if err != nil || !FloatEquals(got, c.want, 6) {
				t.Fatalf("CoerceFloat(%v) = (%v, %v), esperado (~%v, nil)", c.raw, got, err, c.want)
			}
		})
	}

	// "Decimal" não é um tipo dedicado (nem no legado, nem no Go novo) —
	// é Float comparado com tolerância por casas decimais, replicando
	// exatamente base-plugin/types.ts Float.equals. Usa variáveis (não
	// literais) de propósito: uma expressão constante como `19.9 * 3` é
	// calculada pelo compilador Go com precisão arbitrária e arredondada
	// só na conversão final para float64, dando 59.7 exato — não reproduz
	// a multiplicação float64 EM TEMPO DE EXECUÇÃO que tanto o motor JS
	// quanto um valor vindo do host de GO-022 realmente fazem.
	price, qty := 19.9, 3.0
	priceTimesQty := price * qty // 59.70000000000000284217 em IEEE754 real
	if priceTimesQty == 59.7 {
		t.Fatal("premissa do teste inválida: esperava a imprecisão clássica de IEEE754 aqui")
	}
	if !FloatEquals(priceTimesQty, 59.7, 2) {
		t.Errorf("FloatEquals(%v, 59.7, 2) = false, esperado true (tolerância de 2 casas decimais)", priceTimesQty)
	}
	if FloatEquals(59.71, 59.7, 2) {
		t.Error("FloatEquals(59.71, 59.7, 2) = true, esperado false (fora da tolerância de 2 casas decimais)")
	}
}

func TestCoerceBoolean(t *testing.T) {
	tPtr := func(b bool) *bool { return &b }
	cases := []struct {
		name    string
		raw     any
		want    *bool // nil ponteiro == NULL lógico
		wantErr bool
	}{
		{"bool true", true, tPtr(true), false},
		{"bool false", false, tPtr(false), false},
		{`string "TRUE"`, "TRUE", tPtr(true), false},
		{`string "t" minúscula`, "t", tPtr(true), false},
		{`string "ON"`, "ON", tPtr(true), false},
		{`string "yes" minúscula`, "yes", tPtr(true), false},
		{`string "?" é NULL lógico, não erro`, "?", nil, false},
		{`string vazia é NULL lógico, não erro`, "", nil, false},
		{`string desconhecida vira false, não erro (legado é permissivo aqui)`, "talvez", tPtr(false), false},
		{"inteiro não-zero é truthy", 1, tPtr(true), false},
		{"inteiro zero é falsy", 0, tPtr(false), false},
		{"tipo não suportado — erro explícito", []int{1}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CoerceBoolean(c.raw)
			if c.wantErr {
				if !errors.Is(err, ErrCoercionInvalid) {
					t.Fatalf("err = %v, esperado ErrCoercionInvalid", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if (got == nil) != (c.want == nil) {
				t.Fatalf("CoerceBoolean(%v) = %v, esperado %v (nulidade divergente)", c.raw, ptrStr(got), ptrStr(c.want))
			}
			if got != nil && *got != *c.want {
				t.Fatalf("CoerceBoolean(%v) = %v, esperado %v", c.raw, *got, *c.want)
			}
		})
	}
}

func ptrStr(b *bool) string {
	if b == nil {
		return "nil"
	}
	if *b {
		return "&true"
	}
	return "&false"
}

func TestCoerceDate(t *testing.T) {
	rfc3339 := "2026-01-15T10:30:00Z"
	wantRFC3339, _ := time.Parse(time.RFC3339, rfc3339)

	dateOnly := "2026-01-15"
	wantDateOnly, _ := time.Parse("2006-01-02", dateOnly)

	nativeTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	t.Run("RFC3339 completo", func(t *testing.T) {
		got, isNull, err := CoerceDate(rfc3339)
		if err != nil || isNull || !got.Equal(wantRFC3339) {
			t.Fatalf("CoerceDate(%q) = (%v, %v, %v), esperado (%v, false, nil)", rfc3339, got, isNull, err, wantRFC3339)
		}
	})

	t.Run("date-only AAAA-MM-DD", func(t *testing.T) {
		got, isNull, err := CoerceDate(dateOnly)
		if err != nil || isNull || !got.Equal(wantDateOnly) {
			t.Fatalf("CoerceDate(%q) = (%v, %v, %v), esperado (%v, false, nil)", dateOnly, got, isNull, err, wantDateOnly)
		}
	})

	t.Run("time.Time nativo passa direto", func(t *testing.T) {
		got, isNull, err := CoerceDate(nativeTime)
		if err != nil || isNull || !got.Equal(nativeTime) {
			t.Fatalf("CoerceDate(time.Time) = (%v, %v, %v), esperado (%v, false, nil)", got, isNull, err, nativeTime)
		}
	})

	t.Run("formato locale-aware do legado (moment.js) — fora do subconjunto prioritário, erro explícito", func(t *testing.T) {
		_, _, err := CoerceDate("15/01/2026")
		if !errors.Is(err, ErrCoercionUnsupportedFormat) {
			t.Fatalf("err = %v, esperado ErrCoercionUnsupportedFormat — NUNCA um NULL silencioso como o legado faria", err)
		}
	})

	t.Run("string vazia — erro explícito, nunca NULL silencioso", func(t *testing.T) {
		_, _, err := CoerceDate("")
		if !errors.Is(err, ErrCoercionUnsupportedFormat) {
			t.Fatalf("err = %v, esperado ErrCoercionUnsupportedFormat", err)
		}
	})

	t.Run("tipo não suportado — erro explícito", func(t *testing.T) {
		_, _, err := CoerceDate(12345)
		if !errors.Is(err, ErrCoercionInvalid) {
			t.Fatalf("err = %v, esperado ErrCoercionInvalid", err)
		}
	})
}

func TestCoerce_Dispatch_RoundTripPerType(t *testing.T) {
	cases := []struct {
		ft   metadata.FieldType
		raw  any
		want any
	}{
		{metadata.FieldText, "abc", "abc"},
		{metadata.FieldInteger, 3.6, int64(4)},
		{metadata.FieldBoolean, "YES", true},
		{metadata.FieldFloat, "19.9", 19.9},
	}
	for _, c := range cases {
		got, err := Coerce(c.ft, c.raw)
		if err != nil {
			t.Fatalf("Coerce(%s, %v) erro inesperado: %v", c.ft, c.raw, err)
		}
		if got != c.want {
			t.Fatalf("Coerce(%s, %v) = %v, esperado %v", c.ft, c.raw, got, c.want)
		}
	}

	// FieldBoolean coagido de um valor logicamente NULL ("?") devolve nil,
	// não um *bool — o dispatcher desreferencia o ponteiro só quando não é
	// nulo (ver Coerce).
	got, err := Coerce(metadata.FieldBoolean, "?")
	if err != nil || got != nil {
		t.Fatalf("Coerce(FieldBoolean, \"?\") = (%v, %v), esperado (nil, nil)", got, err)
	}
}
