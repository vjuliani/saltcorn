package telemetry

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Este arquivo implementa um registro de métricas mínimo, sem depender de
// um cliente Prometheus externo (ver comentário de pacote em trace.go),
// bastante para os caminhos instrumentados nesta tarefa.
//
// Controle de cardinalidade (critério de aceite de GO-010) é aplicado por
// desenho, não por convenção: toda métrica declara seus nomes de label UMA
// VEZ, na criação (NewCounter/NewHistogram) — não é possível anexar uma
// label nova em tempo de execução. Nenhuma métrica deste pacote usa tenant,
// ator ou qualquer valor de cardinalidade não-limitada como label (isso
// pertenceria aos logs estruturados, que não agregam em séries temporais e
// por isso não sofrem o mesmo problema de explosão de cardinalidade).

// Counter é um contador monotônico, com um conjunto fixo de labels
// declarado na criação.
type Counter struct {
	name       string
	help       string
	labelNames []string

	mu     sync.Mutex
	values map[string]*labeledValue
}

type labeledValue struct {
	labelValues []string
	value       float64
}

// NewCounter cria um contador. labelNames define, de uma vez por todas, as
// labels aceitas — toda chamada a Inc/Add deve fornecer exatamente essa
// quantidade de valores, na mesma ordem.
func NewCounter(name, help string, labelNames ...string) *Counter {
	return &Counter{name: name, help: help, labelNames: labelNames, values: make(map[string]*labeledValue)}
}

// Inc incrementa o contador em 1 para a combinação de labels informada.
func (c *Counter) Inc(labelValues ...string) { c.Add(1, labelValues...) }

// Add incrementa o contador em delta (deve ser não-negativo) para a
// combinação de labels informada. Entra em pânico se o número de valores
// não bater com o número de labels declaradas — erro de programação, não
// de dado em runtime, e deve ser pego em teste/desenvolvimento.
func (c *Counter) Add(delta float64, labelValues ...string) {
	mustMatchLabels(c.name, c.labelNames, labelValues)
	key := labelKey(labelValues)
	c.mu.Lock()
	defer c.mu.Unlock()
	lv, ok := c.values[key]
	if !ok {
		lv = &labeledValue{labelValues: append([]string(nil), labelValues...)}
		c.values[key] = lv
	}
	lv.value += delta
}

func (c *Counter) writeText(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	writeHelpType(w, c.name, c.help, "counter")
	for _, key := range sortedKeys(c.values) {
		lv := c.values[key]
		fmt.Fprintf(w, "%s%s %s\n", c.name, formatLabels(c.labelNames, lv.labelValues), formatFloat(lv.value))
	}
}

// Histogram observa valores contínuos (ex.: duração em segundos) em
// buckets cumulativos, no formato que o Prometheus espera
// (`_bucket{le="..."}`, `_sum`, `_count`).
type Histogram struct {
	name       string
	help       string
	labelNames []string
	bounds     []float64 // limites superiores dos buckets, ordenados; +Inf é implícito

	mu   sync.Mutex
	data map[string]*histogramData
}

type histogramData struct {
	labelValues []string
	bucketCount []uint64 // cumulativo, um por bound (mesma ordem de bounds) + 1 para +Inf
	sum         float64
	count       uint64
}

// NewHistogram cria um histograma com os limites superiores de bucket
// informados (em ordem crescente) — ex.: []float64{0.01, 0.05, 0.1, 0.5, 1, 5}
// para durações em segundos.
func NewHistogram(name, help string, bounds []float64, labelNames ...string) *Histogram {
	sorted := append([]float64(nil), bounds...)
	sort.Float64s(sorted)
	return &Histogram{name: name, help: help, labelNames: labelNames, bounds: sorted, data: make(map[string]*histogramData)}
}

// Observe registra um valor para a combinação de labels informada.
func (h *Histogram) Observe(value float64, labelValues ...string) {
	mustMatchLabels(h.name, h.labelNames, labelValues)
	key := labelKey(labelValues)
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.data[key]
	if !ok {
		d = &histogramData{labelValues: append([]string(nil), labelValues...), bucketCount: make([]uint64, len(h.bounds)+1)}
		h.data[key] = d
	}
	for i, bound := range h.bounds {
		if value <= bound {
			d.bucketCount[i]++
		}
	}
	d.bucketCount[len(h.bounds)]++ // bucket +Inf: sempre incrementado
	d.sum += value
	d.count++
}

func (h *Histogram) writeText(w io.Writer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	writeHelpType(w, h.name, h.help, "histogram")
	for _, key := range sortedHistogramKeys(h.data) {
		d := h.data[key]
		for i, bound := range h.bounds {
			labels := formatLabelsWithExtra(h.labelNames, d.labelValues, "le", formatFloat(bound))
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, labels, d.bucketCount[i])
		}
		infLabels := formatLabelsWithExtra(h.labelNames, d.labelValues, "le", "+Inf")
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, infLabels, d.bucketCount[len(h.bounds)])
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, formatLabels(h.labelNames, d.labelValues), formatFloat(d.sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.name, formatLabels(h.labelNames, d.labelValues), d.count)
	}
}

// GaugeFunc é uma métrica sem label cujo valor é lido sob demanda, no
// momento da exportação — o desenho certo para saturação de recursos (ex.:
// conexões de pool em uso), que reflete o estado atual, não um total
// acumulado.
type GaugeFunc struct {
	name string
	help string
	fn   func() float64
}

// NewGaugeFunc cria uma métrica de leitura sob demanda.
func NewGaugeFunc(name, help string, fn func() float64) *GaugeFunc {
	return &GaugeFunc{name: name, help: help, fn: fn}
}

func (g *GaugeFunc) writeText(w io.Writer) {
	writeHelpType(w, g.name, g.help, "gauge")
	fmt.Fprintf(w, "%s %s\n", g.name, formatFloat(g.fn()))
}

// Registry agrega métricas para exposição no formato de texto do
// Prometheus. Um processo (cmd/server) mantém um Registry e expõe
// Registry.Handler() em GET /metrics.
type Registry struct {
	mu         sync.Mutex
	counters   []*Counter
	histograms []*Histogram
	gaugeFuncs []*GaugeFunc
}

// NewRegistry cria um Registry vazio.
func NewRegistry() *Registry { return &Registry{} }

// RegisterCounter registra c e o retorna, para uso encadeado no ponto de
// criação (ex.: `reqs := registry.RegisterCounter(telemetry.NewCounter(...))`).
func (r *Registry) RegisterCounter(c *Counter) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters = append(r.counters, c)
	return c
}

// RegisterHistogram registra h e o retorna.
func (r *Registry) RegisterHistogram(h *Histogram) *Histogram {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histograms = append(r.histograms, h)
	return h
}

// RegisterGaugeFunc registra g e o retorna.
func (r *Registry) RegisterGaugeFunc(g *GaugeFunc) *GaugeFunc {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gaugeFuncs = append(r.gaugeFuncs, g)
	return g
}

// WriteText escreve todas as métricas registradas no formato de exposição
// de texto do Prometheus.
func (r *Registry) WriteText(w io.Writer) {
	r.mu.Lock()
	counters := append([]*Counter(nil), r.counters...)
	histograms := append([]*Histogram(nil), r.histograms...)
	gaugeFuncs := append([]*GaugeFunc(nil), r.gaugeFuncs...)
	r.mu.Unlock()

	for _, c := range counters {
		c.writeText(w)
	}
	for _, h := range histograms {
		h.writeText(w)
	}
	for _, g := range gaugeFuncs {
		g.writeText(w)
	}
}

// Handler expõe as métricas em GET /metrics, pronto para um coletor
// Prometheus real fazer scrape.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.WriteText(w)
	})
}

func mustMatchLabels(metricName string, names, values []string) {
	if len(names) != len(values) {
		panic(fmt.Sprintf("telemetry: métrica %q declarada com %d labels, chamada com %d valores", metricName, len(names), len(values)))
	}
}

func labelKey(values []string) string {
	return strings.Join(values, "\x00")
}

func sortedKeys(m map[string]*labeledValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedHistogramKeys(m map[string]*histogramData) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeHelpType(w io.Writer, name, help, typ string) {
	if help != "" {
		fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	}
	fmt.Fprintf(w, "# TYPE %s %s\n", name, typ)
}

func formatLabels(names, values []string) string {
	return formatLabelsWithExtra(names, values, "", "")
}

// formatLabelsWithExtra monta o bloco "{k=\"v\",...}" de labels, com um par
// extra opcional no final (usado para o "le" dos buckets de histograma).
func formatLabelsWithExtra(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, name := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(name)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(values[i]))
		b.WriteByte('"')
	}
	if extraName != "" {
		if len(names) > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extraName)
		b.WriteString(`="`)
		b.WriteString(escapeLabelValue(extraValue))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return v
}

func formatFloat(f float64) string {
	if math.IsInf(f, 1) {
		return "+Inf"
	}
	if math.IsInf(f, -1) {
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
