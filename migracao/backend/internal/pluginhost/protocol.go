// Package pluginhost implementa o cliente Go do host temporário de
// extensões JS (GO-022) — o subprocesso Node em
// migracao/packages/pluginhost, que promove o protocolo prototipado em
// docs/migracao-go/prototipos/GO-004-fronteira-rpc/ a produto. Ver
// docs/migracao-go/execucoes/GO-022.md para as decisões de escopo
// completas (por que não vm2, por que só callback de leitura, como o
// isolamento de processo é a fronteira de segurança real — ADR-0005).
package pluginhost

// Capability é o nome de uma capacidade concedida a uma chamada —
// controla quais callbacks o código avaliado no host pode invocar
// (ADR-0005: "capacidades explícitas, não acesso irrestrito"). Um
// callback fora da lista concedida é negado nos DOIS lados (host e
// cliente Go, defesa em profundidade) — ver classifyCallback em client.go.
type Capability string

// CapDBRead é a única capacidade implementada nesta tarefa — um callback
// de LEITURA genérico. Escrita a partir de uma expressão/plugin fica fora
// de escopo (relatório de GO-004 §5: não prototipada, precisa de decisão
// própria de idempotência) — ver nota de escopo 5 em
// docs/migracao-go/execucoes/GO-022.md.
const CapDBRead Capability = "db.read"

// EvalKind distingue "expr" (código de expressão livre) de "call"
// (função já registrada no host, chamada por NOME — nunca por closure
// serializada, achado de GO-004 §4: uma fronteira de processo não
// transporta o ambiente léxico de uma função).
type EvalKind string

const (
	KindExpr EvalKind = "expr"
	KindCall EvalKind = "call"
)

// EvalContext é o contexto serializável exposto à expressão — só
// row/user, nunca um singleton de domínio direto (Table/File/View/User) —
// achado de GO-004 caso #6: sem canal de callback explícito, esses
// singletons viram `undefined` silenciosamente do lado Node.
type EvalContext struct {
	Row  map[string]any `json:"row,omitempty"`
	User map[string]any `json:"user,omitempty"`
}

// EvalRequest é uma chamada ao host — id é atribuído internamente pelo
// Client (ver client.go), nunca pelo chamador.
type EvalRequest struct {
	Kind         EvalKind       `json:"kind"`
	Code         string         `json:"code,omitempty"`
	Name         string         `json:"name,omitempty"`
	Args         map[string]any `json:"args,omitempty"`
	Context      EvalContext    `json:"context,omitempty"`
	Capabilities []Capability   `json:"capabilities"`
	TimeoutMs    int            `json:"timeoutMs,omitempty"`
}

// ErrorCode espelha exatamente os códigos que o host (TypeScript,
// protocol.ts) usa — nunca uma string de erro solta (ADR-0005: protocolo
// de erros).
type ErrorCode string

const (
	ErrCodeRuntime          ErrorCode = "runtime_error"
	ErrCodeCapabilityDenied ErrorCode = "capability_denied"
	ErrCodeInvalidRequest   ErrorCode = "invalid_request"
	ErrCodeTimeout          ErrorCode = "timeout"
	ErrCodeCrashed          ErrorCode = "crashed"
)

// RpcError é o envelope de erro estruturado devolvido pelo host.
type RpcError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// EvalResult é o resultado de uma chamada bem-sucedida OU malsucedida —
// Error é não-nil sse OK é false.
type EvalResult struct {
	OK     bool      `json:"ok"`
	Result any       `json:"result,omitempty"`
	Error  *RpcError `json:"error,omitempty"`
	EvalMs float64   `json:"evalMs"`
}

// incomingMessage decodifica QUALQUER mensagem que o host envia — "result"
// (Error é um objeto estruturado) ou "callback_request" (sem Error algum,
// nunca ambíguo com "result" porque são tipos de mensagem distintos).
// Mensagens que o cliente Go ENVIA usam tipos próprios (evalWire,
// callbackResponseWire) porque "callback_response" carrega um Error como
// STRING solta (mesmo shape de CallbackResponse.error em protocol.ts do
// host) — reaproveitar este mesmo struct para enviar geraria uma colisão
// de chave JSON entre os dois shapes de "error".
type incomingMessage struct {
	Type string `json:"type"`
	ID   int    `json:"id,omitempty"`

	// result
	OK     bool      `json:"ok,omitempty"`
	Result any       `json:"result,omitempty"`
	Error  *RpcError `json:"error,omitempty"`
	EvalMs float64   `json:"evalMs,omitempty"`

	// callback_request
	Corr string         `json:"corr,omitempty"`
	Op   string         `json:"op,omitempty"`
	Args map[string]any `json:"args,omitempty"`
}

// evalWire é a mensagem "eval" enviada ao host — EvalRequest mais o `id`
// atribuído pelo Client (nunca pelo chamador, ver client.go).
type evalWire struct {
	Type string `json:"type"`
	ID   int    `json:"id"`
	EvalRequest
}

// callbackResponseWire é a resposta a um callback_request — Error é uma
// STRING solta (protocol.ts, CallbackResponse.error?: string), não o
// RpcError estruturado usado em "result".
type callbackResponseWire struct {
	Type   string `json:"type"`
	Corr   string `json:"corr"`
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}
