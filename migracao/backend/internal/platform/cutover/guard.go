package cutover

import (
	"context"
	"sync"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// Guard rastreia, em memória, o owner corrente e a admissão/drenagem de
// trabalho em curso por chave (tenant, capability) — o mesmo problema que
// internal/platform/shutdown resolve para o processo inteiro (GO-005), mas
// aqui por chave e resetável: shutdown.Tracker drena uma única vez, de
// forma permanente, para encerrar o processo; Guard precisa drenar e
// voltar a aceitar trabalho repetidamente, uma vez por troca de rota, ao
// longo de toda a vida do processo (rollback e corte de volta podem
// acontecer várias vezes para a mesma tenant/capacidade).
//
// Deliberadamente NÃO consulta o banco a cada Begin: o owner em cache só
// muda via resumeWithOwner, chamado exclusivamente por SwitchOwner depois
// de confirmar a escrita no registro persistido (ver switch.go). Se Begin
// lesse o registro do banco a cada chamada, existiria uma janela real onde
// uma leitura de owner concorrente com um SwitchOwner poderia admitir
// trabalho sob o owner antigo mesmo depois da troca ter sido persistida —
// exatamente o tipo de corrida que o critério de aceite "escritor único"
// de GO-009 proíbe. Mantendo o owner em cache e mudando-o só depois do
// dreno confirmado (dentro da mesma seção crítica que libera admissão
// nova), essa janela não existe.
type Guard struct {
	mu    sync.Mutex
	state map[guardKey]*keyState
}

type guardKey struct {
	tenant     tenancy.Tenant
	capability string
}

type keyState struct {
	n        int
	draining bool
	done     chan struct{}
	owner    Owner // valor zero ("") nunca é igual a OwnerGo — falha fechado por padrão.
}

// NewGuard cria uma Guard pronta para uso, sem nenhuma tenant/capacidade
// registrada — toda chave começa com owner "não é Go" até
// LoadFromRegistry ou SwitchOwner dizerem o contrário.
func NewGuard() *Guard {
	return &Guard{state: make(map[guardKey]*keyState)}
}

// Begin registra uma unidade de trabalho em curso para tenant+capability,
// se e somente se Go for o owner em cache para essa chave e nenhuma troca
// de rota estiver em andamento. A função retornada deve ser chamada
// exatamente uma vez, quando o trabalho terminar. Retorna ErrNotOwner
// (owner em cache não é Go — inclui o caso de nunca ter sido carregado) ou
// ErrRouteDraining (troca de rota em andamento para essa chave).
func (g *Guard) Begin(tenant tenancy.Tenant, capability string) (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	k := guardKey{tenant, capability}
	st := g.stateFor(k)
	if st.draining {
		return nil, ErrRouteDraining
	}
	if st.owner != OwnerGo {
		return nil, ErrNotOwner
	}
	st.n++
	return func() { g.end(k) }, nil
}

// stateFor retorna (criando se necessário) o keyState de k. Chamador já
// deve segurar g.mu.
func (g *Guard) stateFor(k guardKey) *keyState {
	st, ok := g.state[k]
	if !ok {
		st = &keyState{done: make(chan struct{})}
		g.state[k] = st
	}
	return st
}

func (g *Guard) end(k guardKey) {
	g.mu.Lock()
	defer g.mu.Unlock()

	st, ok := g.state[k]
	if !ok {
		return
	}
	st.n--
	if st.n < 0 {
		st.n = 0
	}
	if st.draining && st.n == 0 {
		select {
		case <-st.done:
		default:
			close(st.done)
		}
	}
}

// Drain marca a chave tenant+capability como drenando (nenhum Begin novo é
// aceito a partir daqui, para essa chave) e bloqueia até que todo trabalho
// em curso dessa chave termine ou ctx seja cancelado. Chamar Drain mais de
// uma vez para a mesma chave, sem um resumeWithOwner entre as chamadas, é
// seguro: espera pelo mesmo sinal de conclusão.
func (g *Guard) Drain(ctx context.Context, tenant tenancy.Tenant, capability string) error {
	g.mu.Lock()
	k := guardKey{tenant, capability}
	st := g.stateFor(k)
	st.draining = true
	remaining := st.n
	doneCh := st.done
	g.mu.Unlock()

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

// resumeWithOwner atualiza o owner em cache de tenant+capability e limpa o
// estado de drenagem numa única seção crítica — a atomicidade entre "mudar
// o owner" e "voltar a aceitar trabalho" é o que evita uma janela onde
// Begin admitisse trabalho com draining=false mas ainda o owner antigo (ou
// vice-versa). Chamado só por SwitchOwner, depois que o novo owner já foi
// persistido com sucesso no registro (ver switch.go) — nunca diretamente
// por código fora deste pacote.
func (g *Guard) resumeWithOwner(tenant tenancy.Tenant, capability string, owner Owner) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state[guardKey{tenant, capability}] = &keyState{done: make(chan struct{}), owner: owner}
}

// InFlight retorna quantas unidades de trabalho estão em curso agora para
// tenant+capability. Uso principal: testes — leitura instantânea, não deve
// orientar lógica de negócio.
func (g *Guard) InFlight(tenant tenancy.Tenant, capability string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.state[guardKey{tenant, capability}]; ok {
		return st.n
	}
	return 0
}
