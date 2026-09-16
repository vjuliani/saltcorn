package tenancy

import "regexp"

// nonIdentifierChars replica a sanitização de identificador de
// packages/db-common/internal.ts (`sqlsanitize`): mantém apenas
// letra-Unicode/dígito/underscore, descarta o resto. Reproduzida aqui
// deliberadamente byte-a-byte na semântica (não apenas "parecida") porque
// GO-001 marcou essa regex como crítica para segurança — qualquer divergência
// muda quais nomes de tenant colidem depois de sanitizados.
var nonIdentifierChars = regexp.MustCompile(`[^\p{L}_0-9]`)

// SchemaName converte um Tenant no nome de schema Postgres correspondente.
// Não faz I/O nem valida existência — é só a função pura de sanitização,
// usada tanto para montar o `SET LOCAL search_path` quanto por quem precisar
// prever o nome do schema (ex.: ferramentas de administração futuras).
//
// Resultado nunca é a string vazia nem começa com dígito, para que sempre
// seja um identificador Postgres válido mesmo sem aspas — combinado com
// pgx.Identifier{...}.Sanitize() no pacote database para a citação/escape
// final antes de entrar em qualquer SQL.
func SchemaName(t Tenant) string {
	s := nonIdentifierChars.ReplaceAllString(string(t), "")
	if s == "" {
		return "_"
	}
	if s[0] >= '0' && s[0] <= '9' {
		return "_" + s
	}
	return s
}
