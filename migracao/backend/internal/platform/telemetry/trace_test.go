package telemetry

import (
	"context"
	"errors"
	"testing"
)

func TestNewTraceID_NotZeroAndUnique(t *testing.T) {
	a := NewTraceID()
	b := NewTraceID()
	if a.IsZero() {
		t.Error("NewTraceID() retornou um TraceID zero")
	}
	if a == b {
		t.Error("duas chamadas a NewTraceID() retornaram o mesmo valor")
	}
	if len(a.String()) != 32 {
		t.Errorf("String() len = %d, esperado 32 (16 bytes em hex)", len(a.String()))
	}
}

func TestNewSpanID_NotZeroAndUnique(t *testing.T) {
	a := NewSpanID()
	b := NewSpanID()
	if a.IsZero() {
		t.Error("NewSpanID() retornou um SpanID zero")
	}
	if a == b {
		t.Error("duas chamadas a NewSpanID() retornaram o mesmo valor")
	}
	if len(a.String()) != 16 {
		t.Errorf("String() len = %d, esperado 16 (8 bytes em hex)", len(a.String()))
	}
}

func TestFormatTraceparent_ParseTraceparent_RoundTrip(t *testing.T) {
	traceID := NewTraceID()
	spanID := NewSpanID()
	header := FormatTraceparent(traceID, spanID)

	gotTrace, gotSpan, err := ParseTraceparent(header)
	if err != nil {
		t.Fatalf("ParseTraceparent(%q): %v", header, err)
	}
	if gotTrace != traceID {
		t.Errorf("trace de volta = %s, esperado %s", gotTrace, traceID)
	}
	if gotSpan != spanID {
		t.Errorf("span de volta = %s, esperado %s", gotSpan, spanID)
	}
}

func TestParseTraceparent_KnownGoodExample(t *testing.T) {
	// Exemplo do próprio texto da especificação W3C Trace Context.
	const header = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceID, spanID, err := ParseTraceparent(header)
	if err != nil {
		t.Fatalf("ParseTraceparent: %v", err)
	}
	if traceID.String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("traceID = %s", traceID)
	}
	if spanID.String() != "00f067aa0ba902b7" {
		t.Errorf("spanID = %s", spanID)
	}
}

func TestParseTraceparent_Invalid(t *testing.T) {
	cases := []string{
		"",
		"não-é-um-traceparent",
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", // versão != 00
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",    // faltando flags
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01", // trace-id zero
		"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01", // span-id zero
		"00-curto-00f067aa0ba902b7-01",
	}
	for _, c := range cases {
		if _, _, err := ParseTraceparent(c); !errors.Is(err, ErrInvalidTraceparent) {
			t.Errorf("ParseTraceparent(%q) erro = %v, esperado ErrInvalidTraceparent", c, err)
		}
	}
}

func TestTraceIDFromContext_SpanIDFromContext(t *testing.T) {
	ctx := context.Background()
	if _, ok := TraceIDFromContext(ctx); ok {
		t.Error("TraceIDFromContext em contexto vazio deveria retornar ok=false")
	}

	traceID := NewTraceID()
	spanID := NewSpanID()
	ctx = WithTraceID(ctx, traceID)
	ctx = WithSpanID(ctx, spanID)

	gotTrace, ok := TraceIDFromContext(ctx)
	if !ok || gotTrace != traceID {
		t.Errorf("TraceIDFromContext = %v, %v; esperado %v, true", gotTrace, ok, traceID)
	}
	gotSpan, ok := SpanIDFromContext(ctx)
	if !ok || gotSpan != spanID {
		t.Errorf("SpanIDFromContext = %v, %v; esperado %v, true", gotSpan, ok, spanID)
	}
}
