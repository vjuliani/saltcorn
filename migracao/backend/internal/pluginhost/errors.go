package pluginhost

import "errors"

// Erros sentinela — um por código de ADR-0005 ("protocolo de erros").
// ErrTimeout e ErrCrashed são os dois lados de "timeout/crash... são
// contidos" (critério de aceite de GO-022): em ambos os casos o processo
// do host é morto e a PRÓXIMA chamada sobe um host novo — nunca deixa o
// cliente preso esperando um processo morto ou travado.
var (
	ErrTimeout          = errors.New("pluginhost: tempo de execução excedido")
	ErrCrashed          = errors.New("pluginhost: processo do host encerrou inesperadamente")
	ErrCapabilityDenied = errors.New("pluginhost: capacidade não concedida")
	ErrRuntime          = errors.New("pluginhost: erro de execução no host")
	ErrInvalidRequest   = errors.New("pluginhost: requisição inválida")
	// ErrUnsupportedReference (GO-023) é o lado Go de ErrCodeUnsupportedReference
	// — uma expressão referenciou um singleton de domínio (Table/File/View)
	// sem canal de callback explícito. Distinto de ErrRuntime de propósito:
	// o chamador (internal/expression) precisa diferenciar "a expressão tem
	// um bug" de "a expressão usa uma classe de recurso que este host nunca
	// vai suportar" — só o segundo caso é um bloqueador de migração
	// permanente (ADR-0005), não um erro a corrigir na fórmula.
	ErrUnsupportedReference = errors.New("pluginhost: referência a singleton de domínio não suportada nesta fronteira")
)

// errorForCode traduz o ErrorCode do envelope de erro do host (RpcError)
// para o erro sentinela Go correspondente — o chamador usa errors.Is,
// nunca compara strings.
func errorForCode(code ErrorCode) error {
	switch code {
	case ErrCodeTimeout:
		return ErrTimeout
	case ErrCodeCrashed:
		return ErrCrashed
	case ErrCodeCapabilityDenied:
		return ErrCapabilityDenied
	case ErrCodeInvalidRequest:
		return ErrInvalidRequest
	case ErrCodeUnsupportedReference:
		return ErrUnsupportedReference
	default:
		return ErrRuntime
	}
}
