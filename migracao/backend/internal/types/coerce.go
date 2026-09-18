// Package types implementa coerção explícita de valores BRUTOS (JSON,
// string, number, bool — o que uma expressão avaliada pelo host de GO-022
// devolve, ou o que chegaria de um formulário/CSV) para os tipos básicos de
// metadata.FieldType — o equivalente Go ao read()/readFromDB() de
// packages/saltcorn-data/src/base-plugin/types.ts do legado, restrito ao
// subconjunto prioritário definido em GO-023.
//
// internal/records.validateValue (GO-013) continua sendo a validação
// ESTRITA de valores JÁ tipados em Go (sem nenhuma coerção) — este pacote
// atua numa camada ANTERIOR, só para valores ainda não tipados. As duas
// camadas não se substituem: um `int64` que sai de CoerceInteger ainda
// precisa passar por validateValue como qualquer outro valor de campo.
//
// Onde a semântica do legado diverge deliberadamente da deste pacote (ver
// comentário de cada função), o valor NUNCA é adivinhado silenciosamente —
// um erro explícito e tipado (ErrCoercionInvalid ou
// ErrCoercionUnsupportedFormat) é devolvido, nunca um resultado mansamente
// errado. Ver docs/migracao-go/execucoes/GO-023.md para as decisões de
// escopo completas.
package types

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
)

var (
	// ErrCoercionInvalid é devolvido quando o valor bruto não pode ser
	// coagido para o tipo pedido de NENHUMA forma — o Go rejeita
	// explicitamente onde o legado devolvia `undefined` (um "inválido"
	// implícito, silencioso para quem não checasse o retorno).
	ErrCoercionInvalid = errors.New("types: valor não pode ser coagido para o tipo de campo")
	// ErrCoercionUnsupportedFormat é o fallback explícito para os casos em
	// que o legado tinha um parser mais tolerante — números "sujos" (ex.:
	// "R$ 10,50") ou datas locale-aware via moment.js — que o subconjunto
	// prioritário desta tarefa deliberadamente NÃO reproduz. Nunca um NULL
	// silencioso (que é o que o legado fazia para data inválida): sempre
	// este erro explícito.
	ErrCoercionUnsupportedFormat = errors.New("types: formato fora do subconjunto prioritário suportado (ver GO-023)")
)

// Coerce despacha para a função de coerção do tipo t, tratando nil como
// NULL universal (válido para qualquer tipo, nunca um erro) — o mesmo
// contrato usado por internal/records.validateValue. t == metadata.FieldKey
// ou qualquer valor fora do enum devolve metadata.ErrUnsupportedFieldType:
// este pacote não inventa coerção para relação (FK é sempre um inteiro já
// resolvido pelo catálogo, nunca o alvo de uma expressão de usuário).
func Coerce(t metadata.FieldType, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	switch t {
	case metadata.FieldText:
		return CoerceText(raw)
	case metadata.FieldInteger:
		return CoerceInteger(raw)
	case metadata.FieldBoolean:
		b, err := CoerceBoolean(raw)
		if err != nil || b == nil {
			return nil, err
		}
		return *b, nil
	case metadata.FieldFloat:
		return CoerceFloat(raw)
	case metadata.FieldDate:
		when, isNull, err := CoerceDate(raw)
		if err != nil || isNull {
			return nil, err
		}
		return when, nil
	default:
		return nil, metadata.ErrUnsupportedFieldType
	}
}

// CoerceText replica base-plugin/types.ts String.read: só aceita string Go
// — o legado também não converte number/bool para string
// (`typeof v !== "string"` devolve undefined), aqui um erro explícito no
// lugar do undefined silencioso. Bytes nulos são removidos (colunas text
// do Postgres rejeitam \x00 de qualquer forma).
func CoerceText(raw any) (string, error) {
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: esperado string, recebido %T", ErrCoercionInvalid, raw)
	}
	return strings.ReplaceAll(s, "\x00", ""), nil
}

// CoerceInteger replica base-plugin/types.ts Integer.read: um number Go
// vira int64 arredondado; uma string é parseada como número (equivalente
// ao operador unário `+` do JS, que tolera espaços em volta e notação
// científica) e arredondada; qualquer outro caso é rejeitado
// explicitamente.
func CoerceInteger(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int32:
		return int64(v), nil
	case int64:
		return v, nil
	case float32:
		return coerceFiniteFloatToInt(float64(v))
	case float64:
		return coerceFiniteFloatToInt(v)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%w: string %q não é numérica", ErrCoercionInvalid, v)
		}
		return coerceFiniteFloatToInt(f)
	default:
		return 0, fmt.Errorf("%w: tipo %T não suportado para integer", ErrCoercionInvalid, raw)
	}
}

func coerceFiniteFloatToInt(f float64) (int64, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("%w: valor não finito (%v)", ErrCoercionInvalid, f)
	}
	return int64(math.Round(f)), nil
}

// CoerceFloat aceita números Go diretamente e strings em formato numérico
// ESTRITO (strconv.ParseFloat) — DIVERGÊNCIA DELIBERADA do legado:
// Float.read em base-plugin/types.ts remove caracteres não numéricos de
// uma string antes de parsear (ex.: "R$ 10,50" vira "10.50", mal
// interpretado), um comportamento frágil e implícito. Este subconjunto
// prioritário rejeita explicitamente (ErrCoercionUnsupportedFormat) em vez
// de adivinhar.
func CoerceFloat(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("%w: valor não finito", ErrCoercionInvalid)
		}
		return v, nil
	case float32:
		return CoerceFloat(float64(v))
	case int:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("%w: string %q não está em formato numérico estrito", ErrCoercionUnsupportedFormat, v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("%w: tipo %T não suportado para float", ErrCoercionInvalid, raw)
	}
}

// FloatEquals replica a tolerância de comparação de
// base-plugin/types.ts Float.equals(a,b,{decimal_places}) — necessária
// porque não existe tipo Decimal dedicado nem no legado nem no Go novo:
// "decimal" é Float (double precision / float64, IEEE754) com exibição
// arredondada a decimalPlaces casas; comparar por igualdade exata (`==`)
// é o erro que esta função existe para evitar.
func FloatEquals(a, b float64, decimalPlaces int) bool {
	tol := math.Pow(10, -float64(decimalPlaces)) / 2
	return math.Abs(a-b) <= tol
}

// CoerceBoolean replica base-plugin/types.ts Bool.read — a variante mais
// geral das três funções de coerção booleana do legado
// (readFromFormRecord/readFromDB tratam quirks específicos de formulário
// HTML e do driver Postgres, fora do escopo de um valor de expressão já em
// memória, ver GO-023.md item de escopo sobre limitações). Devolve
// (nil, nil) para NULL lógico — que no legado inclui não só ausência de
// valor, mas também as strings "?" e "" (um valor NÃO-nulo que ainda assim
// significa "sem resposta") — replicado aqui deliberadamente porque é o
// comportamento documentado do legado, não um acidente a corrigir.
func CoerceBoolean(raw any) (*bool, error) {
	switch v := raw.(type) {
	case bool:
		b := v
		return &b, nil
	case string:
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "?", "":
			return nil, nil
		case "TRUE", "T", "ON", "YES":
			t := true
			return &t, nil
		default:
			f := false
			return &f, nil
		}
	case int:
		b := v != 0
		return &b, nil
	case int32:
		b := v != 0
		return &b, nil
	case int64:
		b := v != 0
		return &b, nil
	case float32:
		b := v != 0
		return &b, nil
	case float64:
		b := v != 0
		return &b, nil
	default:
		return nil, fmt.Errorf("%w: tipo %T não suportado para boolean", ErrCoercionInvalid, raw)
	}
}

// CoerceDate replica base-plugin/types.ts Date.read PARCIALMENTE, com uma
// divergência deliberada e documentada: o legado tenta moment() com
// locale, depois new Date(v)/PlainDate, e devolve `null` SILENCIOSAMENTE
// se tudo falhar — exatamente a classe de "resultado muda silenciosamente"
// que o critério de aceite de GO-023 proíbe. Este subconjunto prioritário
// reconhece RFC3339 completo e "AAAA-MM-DD" (date-only, equivalente ao
// attrs.day_only do legado) e devolve ErrCoercionUnsupportedFormat
// explícito para qualquer outro formato — nunca um NULL silencioso.
// isNull só é true quando raw já era logicamente NULL antes de chegar
// aqui (nunca por falha de parse — falha de parse é sempre um erro).
func CoerceDate(raw any) (result time.Time, isNull bool, err error) {
	switch v := raw.(type) {
	case time.Time:
		return v, false, nil
	case string:
		if t, e := time.Parse(time.RFC3339, v); e == nil {
			return t, false, nil
		}
		if t, e := time.Parse("2006-01-02", v); e == nil {
			return t, false, nil
		}
		return time.Time{}, false, fmt.Errorf("%w: string %q não é RFC3339 nem AAAA-MM-DD", ErrCoercionUnsupportedFormat, v)
	default:
		return time.Time{}, false, fmt.Errorf("%w: tipo %T não suportado para date", ErrCoercionInvalid, raw)
	}
}
