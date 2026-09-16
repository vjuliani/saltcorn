// Package telemetry fornece logs estruturados com redação automática de
// dados sensíveis, métricas em formato de exposição Prometheus e
// correlação de trace entre HTTP/SQL/jobs via o padrão W3C Trace Context —
// o mecanismo central do critério de aceite de GO-010 ("uma operação é
// rastreável entre runtimes sem registrar tokens ou dados sensíveis;
// dashboards distinguem falhas e saturação").
//
// Deliberadamente sem dependência de um SDK de tracing/métricas externo
// (OpenTelemetry, cliente Prometheus): a fundação ainda não tem tráfego de
// produção real para justificar o custo de integração e de fixar mais uma
// dependência externa compatível com Go 1.22 (o mesmo cuidado já tomado em
// GO-005/007/008 com pgx/jwt/otp). O formato de trace (W3C Trace Context)
// e de métricas (exposição de texto Prometheus) são padrões abertos —
// qualquer coletor real (Jaeger, Prometheus, Grafana) consegue consumir a
// saída sem exigir um SDK específico.
package telemetry

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
)

// TraceID identifica uma operação de ponta a ponta, propagável entre
// processos via o header padrão W3C `traceparent` — 16 bytes, 32
// caracteres hexadecimais, igual ao padrão W3C Trace Context
// (https://www.w3.org/TR/trace-context/).
type TraceID [16]byte

// SpanID identifica uma unidade de trabalho dentro de um trace (uma
// requisição HTTP, uma transação SQL, um job) — 8 bytes, 16 caracteres
// hexadecimais.
type SpanID [8]byte

var (
	zeroTraceID TraceID
	zeroSpanID  SpanID
)

func (t TraceID) String() string { return hex.EncodeToString(t[:]) }
func (s SpanID) String() string  { return hex.EncodeToString(s[:]) }

// IsZero indica um TraceID/SpanID não inicializado — o W3C Trace Context
// trata explicitamente um trace-id ou parent-id all-zero como inválido
// (ver ParseTraceparent).
func (t TraceID) IsZero() bool { return t == zeroTraceID }
func (s SpanID) IsZero() bool  { return s == zeroSpanID }

// NewTraceID gera um TraceID aleatório. Não é um segredo — só precisa ser
// improvável de colidir entre operações concorrentes, não imprevisível
// contra um adversário — por isso usa math/rand/v2 (rápido, sem risco de
// bloquear a cada chamada) em vez de crypto/rand, diferente de
// internal/identity/token.go, cujo token de API é uma credencial real.
func NewTraceID() TraceID {
	var id TraceID
	putUint64(id[0:8], rand.Uint64())
	putUint64(id[8:16], rand.Uint64())
	return id
}

// NewSpanID gera um SpanID aleatório — mesma justificativa de NewTraceID.
func NewSpanID() SpanID {
	var id SpanID
	putUint64(id[:], rand.Uint64())
	return id
}

func putUint64(b []byte, v uint64) {
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
}

// ErrInvalidTraceparent é retornado por ParseTraceparent quando o header
// não segue o formato W3C Trace Context. Quem chama deve tratar como "sem
// trace recebido" (iniciar um trace novo), nunca como erro fatal — um
// cliente mal-comportado ou um proxy que corrompa o header não pode
// derrubar a observabilidade do backend.
var ErrInvalidTraceparent = errors.New("telemetry: header traceparent inválido")

// ParseTraceparent decodifica um header no formato W3C Trace Context
// ("00-{32 hex}-{16 hex}-{2 hex}"). Só a versão "00" é aceita — versões
// futuras do padrão podem mudar o formato dos campos seguintes, e aceitar
// qualquer versão sem validar o formato seria um contrato implícito que
// esta função não pode garantir.
func ParseTraceparent(header string) (TraceID, SpanID, error) {
	parts := strings.Split(header, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[3]) != 2 {
		return TraceID{}, SpanID{}, ErrInvalidTraceparent
	}

	traceBytes, err := hex.DecodeString(parts[1])
	if err != nil || len(traceBytes) != 16 {
		return TraceID{}, SpanID{}, ErrInvalidTraceparent
	}
	spanBytes, err := hex.DecodeString(parts[2])
	if err != nil || len(spanBytes) != 8 {
		return TraceID{}, SpanID{}, ErrInvalidTraceparent
	}

	var traceID TraceID
	copy(traceID[:], traceBytes)
	var spanID SpanID
	copy(spanID[:], spanBytes)
	if traceID.IsZero() || spanID.IsZero() {
		return TraceID{}, SpanID{}, ErrInvalidTraceparent
	}
	return traceID, spanID, nil
}

// FormatTraceparent monta o header a devolver na resposta (para quem
// chamou correlacionar) ou a propagar numa chamada de saída futura.
func FormatTraceparent(traceID TraceID, spanID SpanID) string {
	return fmt.Sprintf("00-%s-%s-01", traceID, spanID)
}
