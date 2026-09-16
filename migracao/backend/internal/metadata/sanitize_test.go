package metadata

import "testing"

// TestSQLSanitize_KnownGoodCases replica a semântica de
// packages/db-common/internal.ts (`sqlsanitize`) — paridade com o legado
// (rotina de validação de GO-011), incluindo o tratamento de letras Unicode
// fora do ASCII (\p{Letter} no JS, \p{L} no Go).
func TestSQLSanitize_KnownGoodCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"guitars", "guitars"},
		{"my_table", "my_table"},
		{"Table123", "Table123"},
		{"café", "café"},      // letra acentuada Unicode — deve ser preservada
		{"日本語", "日本語"},        // ideogramas — categoria Letter no Unicode
		{"123abc", "_123abc"}, // começa com dígito — prefixo _
		{"", ""},              // vazio continua vazio
		{"   ", ""},           // só espaços — tudo removido
		{"a b c", "abc"},      // espaços internos removidos, não substituídos
		{"a-b_c", "ab_c"},     // hífen removido, underscore preservado
	}
	for _, c := range cases {
		if got := SQLSanitize(c.in); got != c.want {
			t.Errorf("SQLSanitize(%q) = %q, esperado %q", c.in, got, c.want)
		}
	}
}

// TestSQLSanitize_MaliciousNames é a prova direta do critério de aceite
// "nomes maliciosos são cobertos": tentativas de injeção de SQL via nome de
// tabela/campo nunca produzem um identificador com sintaxe SQL residual —
// toda pontuação/símbolo de controle de SQL (`;`, `'`, `"`, espaço, `--`,
// `/*`) é removida, sobrando só o texto alfanumérico.
func TestSQLSanitize_MaliciousNames(t *testing.T) {
	inputs := []string{
		"students; DROP TABLE users;--",
		"' OR 1=1 --",
		`"; DELETE FROM _sc_tables; --`,
		"a\x00b",
		"tabela'); DROP TABLE _sc_tables; --",
	}
	for _, in := range inputs {
		got := SQLSanitize(in)
		for _, forbidden := range []string{";", "'", `"`, " ", "--", "/*", "\x00", "(", ")"} {
			if containsSubstring(got, forbidden) {
				t.Errorf("SQLSanitize(%q) = %q ainda contém caractere perigoso %q", in, got, forbidden)
			}
		}
	}
}

func containsSubstring(s, sub string) bool {
	if sub == "" {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestSQLSanitize_EmptyAfterSanitizationIsEmpty(t *testing.T) {
	// Uma entrada composta só de símbolos deve virar vazio — quem chama
	// (CreateTable/AddField) trata isso como ErrInvalidName, nunca cria um
	// identificador vazio.
	if got := SQLSanitize(";;;---'''"); got != "" {
		t.Errorf("SQLSanitize de entrada só com símbolos = %q, esperado vazio", got)
	}
}

func TestSQLSanitizeAllowDots_PreservesDotsAndQuotes(t *testing.T) {
	got := SQLSanitizeAllowDots(`schema."table"`)
	want := `schema."table"`
	if got != want {
		t.Errorf("SQLSanitizeAllowDots(%q) = %q, esperado %q", `schema."table"`, got, want)
	}
}

func TestSQLSanitizeAllowDots_IsASCIIOnly(t *testing.T) {
	// Diferente de SQLSanitize, a variante AllowDots é ASCII-only no
	// legado — uma letra acentuada não sobrevive.
	if got := SQLSanitizeAllowDots("café"); got != "caf" {
		t.Errorf("SQLSanitizeAllowDots(%q) = %q, esperado %q (ASCII-only)", "café", got, "caf")
	}
}
