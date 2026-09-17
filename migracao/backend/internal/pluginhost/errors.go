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
	default:
		return ErrRuntime
	}
}
