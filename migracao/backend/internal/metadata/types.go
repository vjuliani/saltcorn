package metadata

import "github.com/vjuliani/saltcorn/migracao/backend/internal/identity"

// Table é uma tabela dinâmica registrada no catálogo — o shape mínimo que
// esta tarefa precisa, não o modelo completo de `models/table.ts`.
type Table struct {
	ID           int
	Name         string
	MinRoleRead  identity.RoleID
	MinRoleWrite identity.RoleID
	// Versioned (GO-045, `models/table.ts` — flag `versioned`) — se true,
	// toda escrita física (insert/update/delete) grava um snapshot na
	// tabela `<Name>__history` na MESMA transação, ver
	// internal/records.insertHistoryRow.
	Versioned bool
}

// TableOptions são os parâmetros opcionais de CreateTable. Zero-value usa
// os padrões seguros: leitura pública (identity.RolePublic), escrita só
// admin (identity.RoleAdmin) — o mesmo espírito de "nada é liberado por
// omissão" já usado em internal/cutover (GO-009). Versioned zero-value é
// false — versionamento é opt-in, mesmo padrão do legado.
type TableOptions struct {
	MinRoleRead  identity.RoleID
	MinRoleWrite identity.RoleID
	Versioned    bool
}

// Field é um campo (coluna) de uma tabela dinâmica, registrado no
// catálogo.
type Field struct {
	ID              int
	TableID         int
	Name            string
	Type            FieldType
	Required        bool
	Unique          bool
	ReferencesTable int // ID da tabela referenciada; 0 se Type != FieldKey
}

// FieldDef descreve o campo a ser criado por AddField — a entrada, não o
// registro persistido (que é Field).
type FieldDef struct {
	Name       string
	Type       FieldType
	Required   bool
	Unique     bool
	References string // nome da tabela referenciada; obrigatório quando Type == FieldKey
}
