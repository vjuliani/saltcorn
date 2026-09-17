package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// CallbackFunc resolve um callback de leitura, já autorizado e escopado
// pelo lado Go — nunca uma credencial de banco crua chega ao host
// (ADR-0005). O chamador de Eval decide o que cada Capability realmente
// faz (ex.: CapDBRead chamando internal/records.Rows com o actorRole já
// resolvido) — este pacote não sabe nada sobre tabelas/registros.
type CallbackFunc func(ctx context.Context, args map[string]any) (any, error)

// ClientOptions configura o processo do host. NodeBin/HostScript
// deliberadamente sem padrão embutido — o chamador (cmd/server ou um
// teste) decide onde o binário node e o dist/src/host.js compilado do
// pacote migracao/packages/pluginhost estão, este pacote nunca assume um
// caminho relativo frágil.
type ClientOptions struct {
	NodeBin    string
	HostScript string
	// MaxOldSpaceSizeMB limita o heap do processo Node (--max-old-space-size)
	// — a fronteira de segurança REAL contra um plugin que tenta esgotar
	// memória (ADR-0005: "simples uso de VM não constitui toda a fronteira
	// de segurança" — isto é a fronteira de processo, não a VM). Zero usa o
	// padrão do Node (sem limite explícito) — só para testes que não
	// exercitam contenção de memória.
	MaxOldSpaceSizeMB int
	// Stderr recebe a saída de erro do processo do host (logs, stack
	// traces de falhas do próprio Node) — nunca silenciado.
	Stderr io.Writer
}

// hostConn é a conexão com UMA instância do processo do host — capturada
// por valor local no início de Eval (enquanto c.mu está seguro) e usada
// exclusivamente pela goroutine de leitura daquela chamada. Nunca lê os
// campos mutáveis de Client depois de iniciar: killAndReset troca
// c.stdin/c.stdout/c.proc por uma instância NOVA (ou nil) a qualquer
// momento a partir da goroutine principal de Eval quando o contexto
// expira — sem esta separação, a goroutine de leitura correria com
// killAndReset sobre os mesmos campos sem nenhuma sincronização (bug real
// encontrado e corrigido durante esta tarefa, pego por `go test -race`).
type hostConn struct {
	stdin  io.WriteCloser
	stdout *bufio.Reader
}

func (h hostConn) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("pluginhost: codificar mensagem: %w", err)
	}
	if _, err := h.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("pluginhost: escrever no host: %w", err)
	}
	return nil
}

func (h hostConn) readOne() (incomingMessage, error) {
	line, err := h.stdout.ReadString('\n')
	if err != nil {
		return incomingMessage{}, err
	}
	var msg incomingMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		return incomingMessage{}, fmt.Errorf("pluginhost: decodificar mensagem do host: %w", err)
	}
	return msg, nil
}

// Client gerencia UM processo de host de vida longa (recomendação #1 do
// relatório de GO-004: subprocesso por chamada é proibitivo — 162-176ms de
// warm-up medidos). Chamadas são serializadas (um Eval por vez) —
// simplificação deliberada desta entrega: correlacionar callback_request
// de chamadas CONCORRENTES pelo mesmo processo exigiria rastrear qual
// conjunto de capacidades pertence a qual chamada em voo, complexidade não
// justificada para o volume esperado de um host de extensões (não é
// caminho quente). Uma pool de processos para mais throughput fica como
// extensão futura, não fabricada aqui.
type Client struct {
	opts ClientOptions

	mu   sync.Mutex
	proc *exec.Cmd
	conn hostConn

	nextID atomic.Int64
}

func NewClient(opts ClientOptions) *Client {
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	return &Client{opts: opts}
}

// ensureStarted sobe o processo do host se ainda não estiver rodando —
// chamado sempre sob c.mu. Devolve a hostConn da instância ATUAL (nova ou
// já existente) para o chamador capturar localmente.
func (c *Client) ensureStarted() (hostConn, error) {
	if c.proc != nil {
		return c.conn, nil
	}
	args := []string{}
	if c.opts.MaxOldSpaceSizeMB > 0 {
		args = append(args, fmt.Sprintf("--max-old-space-size=%d", c.opts.MaxOldSpaceSizeMB))
	}
	args = append(args, c.opts.HostScript)

	cmd := exec.Command(c.opts.NodeBin, args...)
	cmd.Stderr = c.opts.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return hostConn{}, fmt.Errorf("pluginhost: obter stdin do host: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return hostConn{}, fmt.Errorf("pluginhost: obter stdout do host: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return hostConn{}, fmt.Errorf("pluginhost: iniciar processo do host: %w", err)
	}

	c.proc = cmd
	c.conn = hostConn{stdin: stdin, stdout: bufio.NewReader(stdout)}
	return c.conn, nil
}

// killAndReset mata o processo atual (se houver) e limpa o estado do
// Client — a PRÓXIMA chamada a Eval sobe um host novo via ensureStarted.
// Chamado tanto em timeout (processo pode estar preso num laço que nem
// vm.Script conseguiu interromper) quanto em crash detectado (leitura do
// stdout falhou) — os dois lados de "timeout/crash... são contidos".
// Sempre chamado sob c.mu.
func (c *Client) killAndReset() {
	if c.proc != nil && c.proc.Process != nil {
		_ = c.proc.Process.Kill()
		_ = c.proc.Wait()
	}
	if c.conn.stdin != nil {
		_ = c.conn.stdin.Close()
	}
	c.proc = nil
	c.conn = hostConn{}
}

// Close encerra o processo do host, se estiver rodando. Seguro chamar
// mesmo se nenhuma chamada foi feita ainda.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.killAndReset()
	return nil
}

// Eval avalia uma expressão/callback registrado no host, servindo
// callback_request que cheguem antes do result correspondente. callbacks
// mapeia cada Capability CONCEDIDA (req.Capabilities) a uma função que a
// resolve do lado Go — uma capacidade ausente do mapa OU ausente de
// req.Capabilities é negada aqui, independentemente do host já ter
// negado do lado dele (defesa em profundidade: os dois lados checam a
// MESMA coisa, nenhum confia cegamente no outro).
func (c *Client) Eval(ctx context.Context, req EvalRequest, callbacks map[Capability]CallbackFunc) (EvalResult, error) {
	c.mu.Lock()
	conn, err := c.ensureStarted()
	if err != nil {
		c.mu.Unlock()
		return EvalResult{}, err
	}

	id := int(c.nextID.Add(1))
	wire := evalWire{Type: "eval", ID: id, EvalRequest: req}
	if err := conn.send(wire); err != nil {
		c.killAndReset()
		c.mu.Unlock()
		return EvalResult{}, fmt.Errorf("%w: %v", ErrCrashed, err)
	}
	c.mu.Unlock() // a goroutine abaixo só usa `conn` (valor local), nunca campos de c — seguro liberar antes dela terminar

	declared := make(map[Capability]bool, len(req.Capabilities))
	for _, capability := range req.Capabilities {
		declared[capability] = true
	}

	type outcome struct {
		result EvalResult
		err    error
	}
	done := make(chan outcome, 1)

	// `conn` é um valor local (capturado por cópia antes de soltar c.mu) —
	// esta goroutine nunca toca c.stdin/c.stdout/c.proc diretamente, então
	// não corre com killAndReset (chamado pelo caminho de ctx.Done() logo
	// abaixo, ou por OUTRA chamada a Eval depois que esta já tiver
	// retornado) — bug de corrida real encontrado e corrigido nesta
	// tarefa, pego por `go test -race` antes de virar regressão.
	go func() {
		for {
			msg, err := conn.readOne()
			if err != nil {
				done <- outcome{err: fmt.Errorf("%w: %v", ErrCrashed, err)}
				return
			}
			switch msg.Type {
			case "callback_request":
				serveCallback(conn, msg, declared, callbacks)
			case "result":
				if msg.ID != id {
					continue // resposta de uma chamada anterior — não deveria acontecer (chamadas são serializadas), nunca travar por isso
				}
				result := EvalResult{OK: msg.OK, Result: msg.Result, Error: msg.Error, EvalMs: msg.EvalMs}
				if !result.OK && result.Error != nil {
					done <- outcome{result: result, err: errorForCode(result.Error.Code)}
					return
				}
				done <- outcome{result: result}
				return
			}
		}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			// Só ErrCrashed exige derrubar o processo — ele já morreu (ou
			// o pipe já quebrou) por conta própria, killAndReset só limpa
			// o estado do Client para a próxima chamada respawnar. Um
			// runtime_error/capability_denied/invalid_request comum NÃO
			// mata o host: ele continua vivo e apto a servir a próxima
			// chamada normalmente — só timeout/crash "contêm" o processo.
			if errors.Is(o.err, ErrCrashed) {
				c.mu.Lock()
				// Só derruba se ninguém mais já reiniciou o processo
				// (ex.: outra chamada, depois desta, já sofreu seu
				// próprio timeout/crash e já reiniciou) — comparar o
				// ponteiro do processo evita matar um host novo por
				// engano.
				if c.proc != nil && sameConn(c.conn, conn) {
					c.killAndReset()
				}
				c.mu.Unlock()
			}
			return o.result, o.err
		}
		return o.result, nil
	case <-ctx.Done():
		c.mu.Lock()
		if c.proc != nil && sameConn(c.conn, conn) {
			c.killAndReset()
		}
		c.mu.Unlock()
		return EvalResult{}, fmt.Errorf("%w: %v", ErrTimeout, ctx.Err())
	}
}

func sameConn(a, b hostConn) bool {
	return a.stdin == b.stdin
}

// serveCallback resolve um callback_request e envia a resposta pela MESMA
// hostConn capturada pela chamada — primeira checagem (declared[op]) é a
// validação Go-side independente da que o host já faz (ver
// requestCallback em host.ts): mesmo que o host tivesse um bug e deixasse
// passar uma capacidade não concedida, este lado nunca invoca a função de
// callback correspondente.
func serveCallback(conn hostConn, msg incomingMessage, declared map[Capability]bool, callbacks map[Capability]CallbackFunc) {
	op := Capability(msg.Op)
	resp := callbackResponseWire{Type: "callback_response", Corr: msg.Corr}

	if !declared[op] {
		resp.Error = fmt.Sprintf("capacidade não concedida (verificação Go): %s", op)
	} else if fn, ok := callbacks[op]; ok {
		result, err := fn(context.Background(), msg.Args)
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = result
		}
	} else {
		resp.Error = fmt.Sprintf("nenhum resolvedor de callback registrado para a capacidade: %s", op)
	}

	// Erro de transporte ao responder o callback não tem para onde
	// propagar aqui (é uma goroutine de leitura) — o Eval() que está
	// esperando o result vai estourar em timeout/crash naturalmente se o
	// host ficar sem resposta, sem precisar de um segundo canal de erro.
	_ = conn.send(resp)
}
