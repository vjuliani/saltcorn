// GO-004 — caller Go do experimento de fronteira RPC Go<->Node.
//
// Sobe host.cjs como subprocesso, envia uma amostra de fórmulas/callbacks/tipos
// customizados/plugin-com-banco pela fronteira, mede a latência round-trip de
// cada chamada e grava um relatório em JSON (results.json) usado pelo relatório
// em ../../relatorio-go-004.md. Não é código de produto — ver nota em
// docs/migracao-go/execucoes/GO-004.md sobre por que este protótipo fica fora
// de migracao/.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"
)

type Req struct {
	ID      int                    `json:"id"`
	Kind    string                 `json:"kind"`
	Code    string                 `json:"code,omitempty"`
	Name    string                 `json:"name,omitempty"`
	Args    map[string]interface{} `json:"args,omitempty"`
	Context map[string]interface{} `json:"context,omitempty"`
}

type Msg struct {
	Type   string                 `json:"type"`
	ID     int                    `json:"id"`
	OK     bool                   `json:"ok"`
	Result json.RawMessage        `json:"result"`
	Error  string                 `json:"error"`
	EvalMs float64                `json:"evalMs"`
	Corr   string                 `json:"corr"`
	Op     string                 `json:"op"`
	Args   map[string]interface{} `json:"args"`
}

type CaseResult struct {
	ID          int     `json:"id"`
	Description string  `json:"description"`
	Kind        string  `json:"kind"`
	OK          bool    `json:"ok"`
	Result      string  `json:"result,omitempty"`
	Error       string  `json:"error,omitempty"`
	RoundTripMs float64 `json:"round_trip_ms"`
	EvalMs      float64 `json:"eval_ms"`
}

// fakeDB simula, do lado Go, a única tabela que o experimento consulta via
// callback. Deliberadamente em memória (não Postgres real) — ver limitação
// registrada no relatório: isto mede a mecânica e o custo do callback, não o
// custo real de uma consulta SQL.
var fakeDB = map[string]map[string]interface{}{
	"guitars": {"id": float64(1), "name": "guitars", "brand": "Fender"},
}

type bridge struct {
	stdin  *bufio.Writer
	stdout *bufio.Reader
}

func (b *bridge) send(v interface{}) {
	bz, err := json.Marshal(v)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	b.stdin.Write(bz)
	b.stdin.WriteByte('\n')
	b.stdin.Flush()
}

func (b *bridge) readMsg() Msg {
	line, err := b.stdout.ReadString('\n')
	if err != nil {
		log.Fatalf("read from host.cjs: %v", err)
	}
	var m Msg
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		log.Fatalf("unmarshal %q: %v", line, err)
	}
	return m
}

// runOne envia uma requisição e serve quaisquer callback_request que cheguem
// antes do result correspondente — o "servidor de banco" do lado Go.
func (b *bridge) runOne(req Req) (Msg, time.Duration) {
	start := time.Now()
	b.send(req)
	for {
		m := b.readMsg()
		if m.Type == "callback_request" {
			name, _ := m.Args["name"].(string)
			row, found := fakeDB[name]
			resp := map[string]interface{}{"type": "callback_response", "corr": m.Corr}
			if found {
				resp["result"] = row
			} else {
				resp["error"] = "not found"
			}
			b.send(resp)
			continue
		}
		if m.Type == "result" && m.ID == req.ID {
			return m, time.Since(start)
		}
	}
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

func main() {
	cmd := exec.Command("node", "host.cjs")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
	}

	b := &bridge{stdin: bufio.NewWriter(stdin), stdout: bufio.NewReader(stdout)}

	type Case struct {
		ID          int
		Description string
		Req         Req
	}

	cases := []Case{
		{1, "aritmética pura (row.price * row.qty)", Req{Kind: "expr", Code: "row.price * row.qty",
			Context: map[string]interface{}{"row": map[string]interface{}{"price": 19.9, "qty": 3}}}},
		{2, "concatenação de string (row.first_name + row.last_name)", Req{Kind: "expr", Code: "row.first_name + ' ' + row.last_name",
			Context: map[string]interface{}{"row": map[string]interface{}{"first_name": "Ada", "last_name": "Lovelace"}}}},
		{3, "condicional sobre user.role_id", Req{Kind: "expr", Code: "user && user.role_id === 1 ? 'admin' : 'nao-admin'",
			Context: map[string]interface{}{"user": map[string]interface{}{"role_id": 1}}}},
		{4, "Date como resultado (round-trip JSON vira string)", Req{Kind: "expr", Code: "new Date('2026-01-15T00:00:00Z')",
			Context: map[string]interface{}{}}},
		{5, "getFullYear sobre string de data", Req{Kind: "expr", Code: "new Date(row.created_at).getFullYear()",
			Context: map[string]interface{}{"row": map[string]interface{}{"created_at": "2024-05-01"}}}},
		{6, "singleton Table indisponível por padrão nesta sandbox mínima", Req{Kind: "expr", Code: "typeof Table",
			Context: map[string]interface{}{}}},
		{7, "callback nomeado: ação de plugin (toast)", Req{Kind: "call", Name: "sendToast",
			Args: map[string]interface{}{"msg": "ola"}}},
		{8, "callback nomeado: tipo customizado (currency read)", Req{Kind: "call", Name: "customType_currency_read",
			Args: map[string]interface{}{"value": "10.5", "attrs": map[string]interface{}{"currency": "USD"}}}},
		{9, "caso negativo: função com closure de variável externa nunca enviada", Req{Kind: "raw_fn_attempt",
			Code: "function(){ return outerCounter + 1; }"}},
		{10, "expressão dependente de banco via callback (Table.findOne, registro existente)", Req{Kind: "db",
			Code: "await Table.findOne({name: 'guitars'})"}},
		{11, "expressão dependente de banco via callback (registro inexistente)", Req{Kind: "db",
			Code: "await Table.findOne({name: 'nao-existe'})"}},
	}

	var results []CaseResult
	for _, c := range cases {
		c.Req.ID = c.ID
		m, dur := b.runOne(c.Req)
		cr := CaseResult{
			ID: c.ID, Description: c.Description, Kind: c.Req.Kind,
			OK: m.OK, Error: m.Error, RoundTripMs: ms(dur), EvalMs: m.EvalMs,
		}
		if m.OK {
			cr.Result = string(m.Result)
		}
		results = append(results, cr)
		status := "OK"
		if !m.OK {
			status = "FALHA"
		}
		fmt.Printf("#%-2d [%-4s] %-5s round_trip=%7.3fms eval=%7.3fms  %s\n",
			c.ID, c.Req.Kind, status, cr.RoundTripMs, m.EvalMs, c.Description)
		if !m.OK {
			fmt.Printf("     erro: %s\n", m.Error)
		} else {
			fmt.Printf("     resultado: %s\n", cr.Result)
		}
	}

	// Benchmarks de custo da ponte. Cada um tem uma fase de aquecimento
	// descartada antes de medir — sem isso, o primeiro benchmark da vez
	// absorve o custo de warm-up do V8/vm2 e distorce a comparação entre os
	// dois (a primeira rodada observada tinha DB "mais rápido" que puro só
	// por rodar depois de 200 chamadas de aquecimento implícito).
	const warmup = 30
	for i := 0; i < warmup; i++ {
		b.runOne(Req{ID: 9000 + i, Kind: "expr", Code: "1+1", Context: map[string]interface{}{}})
	}

	const nPure = 200
	var totalPure, minPure, maxPure time.Duration
	for i := 0; i < nPure; i++ {
		_, dur := b.runOne(Req{ID: 1000 + i, Kind: "expr", Code: "1+1", Context: map[string]interface{}{}})
		totalPure += dur
		if i == 0 || dur < minPure {
			minPure = dur
		}
		if dur > maxPure {
			maxPure = dur
		}
	}
	avgPure := totalPure / time.Duration(nPure)

	for i := 0; i < warmup; i++ {
		b.runOne(Req{ID: 9500 + i, Kind: "db", Code: "await Table.findOne({name: 'guitars'})"})
	}

	// Benchmark de custo com 1 callback de banco por chamada.
	const nDB = 200
	var totalDB, minDB, maxDB time.Duration
	for i := 0; i < nDB; i++ {
		_, dur := b.runOne(Req{ID: 2000 + i, Kind: "db", Code: "await Table.findOne({name: 'guitars'})"})
		totalDB += dur
		if i == 0 || dur < minDB {
			minDB = dur
		}
		if dur > maxDB {
			maxDB = dur
		}
	}
	avgDB := totalDB / time.Duration(nDB)

	fmt.Printf("\nBenchmark de latência da ponte (após %d chamadas de aquecimento descartadas em cada rodada):\n", warmup)
	fmt.Printf("  N=%d chamada pura trivial ('1+1'): média=%.3fms min=%.3fms max=%.3fms\n",
		nPure, ms(avgPure), ms(minPure), ms(maxPure))
	fmt.Printf("  N=%d chamada com 1 callback de banco: média=%.3fms min=%.3fms max=%.3fms (overhead do callback sobre a média pura: %.3fms)\n",
		nDB, ms(avgDB), ms(minDB), ms(maxDB), ms(avgDB)-ms(avgPure))

	out := map[string]interface{}{
		"cases": results,
		"warmup_calls_discarded": warmup,
		"benchmark_pure": map[string]interface{}{
			"n": nPure, "avg_ms": ms(avgPure), "min_ms": ms(minPure), "max_ms": ms(maxPure),
		},
		"benchmark_db_callback": map[string]interface{}{
			"n": nDB, "avg_ms": ms(avgDB), "min_ms": ms(minDB), "max_ms": ms(maxDB), "overhead_over_pure_ms": ms(avgDB) - ms(avgPure),
		},
	}
	f, err := os.Create("results.json")
	if err != nil {
		log.Fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		log.Fatal(err)
	}
	f.Close()

	stdin.Close()
	cmd.Wait()
}
