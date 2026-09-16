package telemetry

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestCounter_IncAndAdd(t *testing.T) {
	c := NewCounter("test_counter", "ajuda", "method", "route")
	c.Inc("GET", "tenant_records")
	c.Inc("GET", "tenant_records")
	c.Add(3, "POST", "tenant_records")

	var buf bytes.Buffer
	c.writeText(&buf)
	out := buf.String()

	if !strings.Contains(out, `test_counter{method="GET",route="tenant_records"} 2`) {
		t.Errorf("saída não contém a contagem esperada para GET:\n%s", out)
	}
	if !strings.Contains(out, `test_counter{method="POST",route="tenant_records"} 3`) {
		t.Errorf("saída não contém a contagem esperada para POST:\n%s", out)
	}
	if !strings.Contains(out, "# TYPE test_counter counter") {
		t.Errorf("saída não contém a linha TYPE:\n%s", out)
	}
}

func TestCounter_WrongLabelCountPanics(t *testing.T) {
	c := NewCounter("test_counter", "ajuda", "method", "route")
	defer func() {
		if recover() == nil {
			t.Error("Inc com número errado de labels deveria entrar em pânico")
		}
	}()
	c.Inc("GET") // faltando "route"
}

func TestCounter_ConcurrentIncNoRace(t *testing.T) {
	c := NewCounter("test_counter", "ajuda", "result")
	var wg sync.WaitGroup
	const workers = 50
	const iterations = 100
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				c.Inc("ok")
			}
		}()
	}
	wg.Wait()

	var buf bytes.Buffer
	c.writeText(&buf)
	if !strings.Contains(buf.String(), `test_counter{result="ok"} 5000`) {
		t.Errorf("contagem final incorreta:\n%s", buf.String())
	}
}

func TestHistogram_ObserveAndWriteText(t *testing.T) {
	h := NewHistogram("test_duration_seconds", "ajuda", []float64{0.1, 0.5, 1})
	h.Observe(0.05)
	h.Observe(0.2)
	h.Observe(0.2)
	h.Observe(2.0)

	var buf bytes.Buffer
	h.writeText(&buf)
	out := buf.String()

	// bucket le=0.1: só a observação de 0.05 (1)
	if !strings.Contains(out, `test_duration_seconds_bucket{le="0.1"} 1`) {
		t.Errorf("bucket le=0.1 incorreto:\n%s", out)
	}
	// bucket le=0.5: 0.05, 0.2, 0.2 (3, cumulativo)
	if !strings.Contains(out, `test_duration_seconds_bucket{le="0.5"} 3`) {
		t.Errorf("bucket le=0.5 incorreto:\n%s", out)
	}
	// bucket le=+Inf: todas as 4 observações
	if !strings.Contains(out, `test_duration_seconds_bucket{le="+Inf"} 4`) {
		t.Errorf("bucket le=+Inf incorreto:\n%s", out)
	}
	if !strings.Contains(out, "test_duration_seconds_count 4") {
		t.Errorf("count incorreto:\n%s", out)
	}
	wantSum := 0.05 + 0.2 + 0.2 + 2.0
	if !strings.Contains(out, "test_duration_seconds_sum "+formatFloat(wantSum)) {
		t.Errorf("sum incorreto (esperado %v):\n%s", wantSum, out)
	}
}

func TestHistogram_WithLabels(t *testing.T) {
	h := NewHistogram("test_duration_seconds", "ajuda", []float64{1}, "method")
	h.Observe(0.5, "GET")
	h.Observe(5, "POST")

	var buf bytes.Buffer
	h.writeText(&buf)
	out := buf.String()
	if !strings.Contains(out, `test_duration_seconds_bucket{method="GET",le="1"} 1`) {
		t.Errorf("bucket com label GET incorreto:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{method="POST",le="1"} 0`) {
		t.Errorf("bucket com label POST (le=1) deveria ser 0:\n%s", out)
	}
	if !strings.Contains(out, `test_duration_seconds_bucket{method="POST",le="+Inf"} 1`) {
		t.Errorf("bucket +Inf com label POST incorreto:\n%s", out)
	}
}

func TestGaugeFunc_ReadsOnDemand(t *testing.T) {
	value := 3.0
	g := NewGaugeFunc("test_gauge", "ajuda", func() float64 { return value })

	var buf bytes.Buffer
	g.writeText(&buf)
	if !strings.Contains(buf.String(), "test_gauge 3") {
		t.Errorf("valor inicial incorreto:\n%s", buf.String())
	}

	value = 7.0
	buf.Reset()
	g.writeText(&buf)
	if !strings.Contains(buf.String(), "test_gauge 7") {
		t.Errorf("GaugeFunc não refletiu o novo valor (deveria ler sob demanda):\n%s", buf.String())
	}
}

func TestRegistry_WriteTextIncludesAllRegistered(t *testing.T) {
	r := NewRegistry()
	counter := r.RegisterCounter(NewCounter("c_total", "ajuda"))
	counter.Inc()
	hist := r.RegisterHistogram(NewHistogram("h_seconds", "ajuda", []float64{1}))
	hist.Observe(0.5)
	r.RegisterGaugeFunc(NewGaugeFunc("g_value", "ajuda", func() float64 { return 42 }))

	var buf bytes.Buffer
	r.WriteText(&buf)
	out := buf.String()
	for _, want := range []string{"c_total 1", "h_seconds_count 1", "g_value 42"} {
		if !strings.Contains(out, want) {
			t.Errorf("saída do Registry não contém %q:\n%s", want, out)
		}
	}
}

func TestEscapeLabelValue(t *testing.T) {
	got := escapeLabelValue(`linha com "aspas" e \barra`)
	want := `linha com \"aspas\" e \\barra`
	if got != want {
		t.Errorf("escapeLabelValue = %q, esperado %q", got, want)
	}
}
