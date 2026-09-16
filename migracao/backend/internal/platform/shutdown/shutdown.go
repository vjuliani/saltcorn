// Package shutdown rastreia unidades de trabalho em curso (uma requisição
// sendo atendida, um job sendo processado) para que um encerramento gracioso
// espere esse trabalho terminar em vez de matá-lo no meio — o critério de
// aceite de GO-005 "processo encerra sem perder transações em curso".
package shutdown

import (
	"context"
	"errors"
	"sync"
)

// ErrDraining é retornado por Begin quando o Tracker já está drenando —
// quem chama deve recusar o trabalho novo (ex.: responder 503 a uma
// requisição HTTP) em vez de iniciá-lo.
var ErrDraining = errors.New("shutdown em andamento: novo trabalho recusado")

// Tracker conta unidades de trabalho em curso. Seguro para uso concorrente.
type Tracker struct {
	mu       sync.Mutex
	n        int
	draining bool
	done     chan struct{}
}

// NewTracker cria um Tracker pronto para uso.
func NewTracker() *Tracker {
	return &Tracker{done: make(chan struct{})}
}

// Begin registra uma unidade de trabalho em curso. A função retornada deve
// ser chamada exatamente uma vez, quando o trabalho terminar — normalmente
// via defer. Retorna ErrDraining se o Tracker já estiver drenando: o chamador
// não deve iniciar o trabalho, apenas propagar a recusa.
func (t *Tracker) Begin() (func(), error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.draining {
		return nil, ErrDraining
	}
	t.n++
	return t.end, nil
}

func (t *Tracker) end() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.n--
	if t.n < 0 {
		// Não deveria acontecer (end chamado mais de uma vez para o mesmo
		// Begin) — não entra em pânico em produção, mas também não deixa a
		// contagem ficar negativa silenciosamente.
		t.n = 0
	}
	if t.draining && t.n == 0 {
		select {
		case <-t.done:
			// já fechado (Drain chamado com n==0 originalmente ou fechado antes)
		default:
			close(t.done)
		}
	}
}

// Drain marca o Tracker como drenando (nenhum Begin novo é aceito a partir
// daqui) e bloqueia até que todo trabalho em curso termine ou ctx seja
// cancelado — o que ocorrer primeiro. Retorna ctx.Err() no caso de timeout;
// o trabalho pode continuar rodando em segundo plano mesmo assim, cabe ao
// chamador decidir se força a saída do processo de qualquer forma.
//
// Chamar Drain mais de uma vez é seguro (idempotente): chamadas subsequentes
// apenas esperam pelo mesmo sinal de conclusão.
func (t *Tracker) Drain(ctx context.Context) error {
	t.mu.Lock()
	t.draining = true
	remaining := t.n
	doneCh := t.done
	t.mu.Unlock()

	if remaining == 0 {
		return nil
	}

	select {
	case <-doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// InFlight retorna quantas unidades de trabalho estão em curso agora. Uso
// principal: testes e métricas — não deve orientar lógica de negócio (é uma
// leitura instantânea, pode mudar logo em seguida).
func (t *Tracker) InFlight() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.n
}
