package telemetry

import "context"

type contextKey int

const (
	traceIDKey contextKey = iota
	spanIDKey
	loggerKey
)

// WithTraceID retorna um contexto derivado carregando o TraceID informado.
func WithTraceID(ctx context.Context, id TraceID) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}

// TraceIDFromContext lê o TraceID do contexto, se presente.
func TraceIDFromContext(ctx context.Context) (TraceID, bool) {
	id, ok := ctx.Value(traceIDKey).(TraceID)
	return id, ok
}

// WithSpanID retorna um contexto derivado carregando o SpanID informado —
// cada camada instrumentada (HTTP, SQL, job) deve gerar seu próprio SpanID
// ao entrar, mantendo o mesmo TraceID do contexto pai.
func WithSpanID(ctx context.Context, id SpanID) context.Context {
	return context.WithValue(ctx, spanIDKey, id)
}

// SpanIDFromContext lê o SpanID do contexto, se presente.
func SpanIDFromContext(ctx context.Context) (SpanID, bool) {
	id, ok := ctx.Value(spanIDKey).(SpanID)
	return id, ok
}
