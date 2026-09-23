package workflow

// Workflow é o registro persistido que identifica um grafo de passos —
// o "host" que o legado resolvia emprestando um Trigger com
// when_trigger ∈ {"API call","Never"} (ver docs/migracao-go/execucoes/
// GO-048.md, decisão de escopo #1: aqui é uma tabela própria, não uma
// sobrecarga de _sc_triggers, que exige table_id NOT NULL). Version é o
// token de concorrência otimista (xmin::text), mesma convenção de
// internal/views.View.
type Workflow struct {
	ID          int
	Name        string
	InitialStep string
	Version     string
}

// StoredStep é um passo persistido de um Workflow — o shape que o editor
// visual lê/escreve. Os campos OnlyIf/NextStep/ElseStep/ErrorStep
// espelham exatamente Step (definition.go); PositionX/PositionY são só
// layout de canvas, nunca lidos por Compile.
type StoredStep struct {
	ID            int
	WorkflowID    int
	Name          string
	ActionName    string
	Configuration map[string]any
	OnlyIf        string
	NextStep      string
	ElseStep      string
	ErrorStep     string
	PositionX     float64
	PositionY     float64
	Version       string
}
