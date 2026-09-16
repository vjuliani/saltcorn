// Package metadata implementa o catálogo de tabelas/campos/relações
// dinâmicos e o executor de evolução de schema (GO-011) — o núcleo do
// produto na matriz de capacidades GO-001 §2.2 (`models/table.ts`,
// `models/field.ts`). Toda mutação de catálogo (criar/alterar/remover
// tabela ou campo) grava o metadado e executa a DDL correspondente na
// MESMA transação Postgres (via internal/platform/database), de forma que
// uma falha a meio do caminho desfaz os dois lados juntos — nunca um
// catálogo que descreve uma coluna que não existe fisicamente, ou uma
// coluna física sem registro no catálogo.
package metadata

import "regexp"

// disallowedChars remove tudo que não seja letra Unicode, dígito ou
// underscore — \p{L} no Go é a mesma categoria Unicode "Letter" que
// \p{Letter} no JavaScript, então esta expressão é semanticamente
// equivalente à do legado.
var disallowedChars = regexp.MustCompile(`[^\p{L}_0-9]`)

// disallowedCharsAllowDots é a variante ASCII-only (não Unicode) que também
// permite ponto e aspas duplas — usada só para identificadores já
// qualificados/citados (schema.tabela, "coluna"), nunca para um nome
// definido pelo usuário final que vira uma única tabela/coluna.
var disallowedCharsAllowDots = regexp.MustCompile(`[^A-Za-z_0-9."]`)

// SQLSanitize porta byte-a-byte (incl. semântica Unicode) a função
// `sqlsanitize` de `packages/db-common/internal.ts`: remove todo caractere
// que não seja letra Unicode, dígito ou underscore, e prefixa com `_` se o
// resultado começar com dígito — nunca use um nome definido pelo usuário
// final (nome de tabela, nome de campo) como identificador SQL sem passar
// por esta função primeiro. Uma entrada composta só por caracteres
// removidos (ex.: só símbolos, ou vazia) retorna string vazia — quem chama
// deve tratar isso como nome inválido, nunca criar um identificador vazio.
func SQLSanitize(name string) string {
	s := disallowedChars.ReplaceAllString(name, "")
	return prefixIfStartsWithDigit(s)
}

// SQLSanitizeAllowDots porta `sqlsanitizeAllowDots`: mesma ideia, mas
// ASCII-only e permitindo `.` e `"` — para os poucos casos em que um
// identificador já qualificado (schema.tabela) precisa ser preservado.
func SQLSanitizeAllowDots(name string) string {
	s := disallowedCharsAllowDots.ReplaceAllString(name, "")
	return prefixIfStartsWithDigit(s)
}

func prefixIfStartsWithDigit(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= '0' && s[0] <= '9' {
		return "_" + s
	}
	return s
}
