package telemetry

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// sensitiveKeyFragments são trechos (comparados sem diferenciar
// maiúsculas/minúsculas) que, se aparecerem no NOME de um atributo de log,
// fazem o valor ser substituído por "REDACTED" antes de sair — a garantia
// estrutural de "sem registrar tokens ou dados sensíveis" (critério de
// aceite de GO-010): uma checagem no handler de log, não uma convenção que
// depende de quem escreve cada chamada de log lembrar de não incluir o
// campo errado.
var sensitiveKeyFragments = []string{
	"token", "password", "senha", "secret", "segredo",
	"authorization", "cookie", "totp_secret",
}

func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	for _, frag := range sensitiveKeyFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, "REDACTED")
	}
	return a
}

// NewHandler cria um slog.Handler em JSON com redação automática de
// atributos sensíveis por nome — o handler padrão usado por cmd/server,
// cmd/worker e cmd/cli para log estruturado.
func NewHandler(w io.Writer, level slog.Leveler) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactAttr,
	})
}

// WithLogger anexa um *slog.Logger base ao contexto — chamado uma vez,
// perto da raiz de uma operação (main, o início de uma requisição HTTP ou
// de um ciclo de job), para que LoggerFor em qualquer ponto mais profundo
// da mesma operação use o mesmo handler/destino sem precisar recebê-lo
// como parâmetro explícito em cada função.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// LoggerFor retorna um logger com os atributos de correlação disponíveis
// no contexto já anexados: trace_id (se presente) e tenant/ator (se
// presentes via internal/platform/tenancy) — o ponto único que todo
// caminho de código instrumentado (HTTP, SQL, job) deve usar, para que uma
// mesma operação seja correlacionável entre linhas de log e entre
// runtimes. Sem um logger anexado ao contexto (ex.: chamado fora de
// qualquer requisição/job instrumentado), cai para slog.Default().
func LoggerFor(ctx context.Context) *slog.Logger {
	logger, ok := ctx.Value(loggerKey).(*slog.Logger)
	if !ok || logger == nil {
		logger = slog.Default()
	}
	if traceID, ok := TraceIDFromContext(ctx); ok {
		logger = logger.With("trace_id", traceID.String())
	}
	if spanID, ok := SpanIDFromContext(ctx); ok {
		logger = logger.With("span_id", spanID.String())
	}
	if tenant, ok := tenancy.TenantFromContext(ctx); ok {
		logger = logger.With("tenant", string(tenant))
	}
	if actor, ok := tenancy.ActorFromContext(ctx); ok {
		logger = logger.With("actor", actor)
	}
	return logger
}
