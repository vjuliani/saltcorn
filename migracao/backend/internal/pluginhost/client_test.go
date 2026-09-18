// Testes de integração do cliente Go do host de extensões (GO-022) —
// sobem o host REAL compilado (migracao/packages/pluginhost/dist/src/
// host.js), não um dublê. Requer `node` no PATH e o pacote já compilado
// (`npm run build` em migracao/packages/pluginhost) — mesma convenção de
// migracao/packages/bff: "os testes rodam contra dist/ compilado".
package pluginhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/records"
)

func hostScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller falhou")
	}
	// internal/pluginhost/client_test.go -> ../../../packages/pluginhost/dist/src/host.js
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "packages", "pluginhost", "dist", "src", "host.js")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("host.js compilado não encontrado em %s — rode `npm run build` em migracao/packages/pluginhost primeiro: %v", path, err)
	}
	return path
}

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c := NewClient(ClientOptions{NodeBin: "node", HostScript: hostScriptPath(t)})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestEval_PureExpression(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.Eval(ctx, EvalRequest{
		Kind:         KindExpr,
		Code:         "row.price * row.qty",
		Context:      EvalContext{Row: map[string]any{"price": 19.9, "qty": 3}},
		Capabilities: nil,
	}, nil)
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false, erro = %+v", res.Error)
	}
	got, _ := res.Result.(float64)
	if got < 59.69 || got > 59.71 {
		t.Errorf("res.Result = %v, esperado ~59.7", res.Result)
	}
}

func TestEval_NamedCall(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.Eval(ctx, EvalRequest{Kind: KindCall, Name: "sendToast", Args: map[string]any{"msg": "ola"}}, nil)
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false, erro = %+v", res.Error)
	}
	m, _ := res.Result.(map[string]any)
	if m["message"] != "Toast: ola" {
		t.Errorf("res.Result = %+v, esperado message=Toast: ola", res.Result)
	}
}

// TestEval_DBReadCallback_RealAuthorization prova o callback de leitura
// de ponta a ponta contra Postgres real, reaproveitando internal/records
// e internal/identity — nenhuma checagem de autorização nova inventada
// (nota de escopo 4 em docs/migracao-go/execucoes/GO-022.md). Um ator
// público (sem acesso à tabela) tem o callback negado pela MESMA
// checagem que qualquer outra leitura no backend já usa (GO-015).
func TestEval_DBReadCallback_RealAuthorization(t *testing.T) {
	db := testDB(t)
	tenant, tableID := testFixture(t, db)
	seedBook(t, db, tenant, tableID, "Dune")

	c := newTestClient(t)

	// dbReadCallback é o resolvedor real da capacidade "db.read" — chama
	// internal/records.Rows (GO-012/013/015) dentro da MESMA transação do
	// tenant, com o actorRole explícito do chamador. Nenhuma checagem de
	// autorização nova: é exatamente o que qualquer rota HTTP do backend
	// já faz (cmd/server/records.go).
	dbReadCallback := func(actorRole identity.RoleID) CallbackFunc {
		return func(ctx context.Context, args map[string]any) (any, error) {
			var rows []map[string]any
			err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				rows, err = records.Rows(ctx, tx, actorRole, records.Query{Table: "books", Limit: 1})
				return err
			})
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				return nil, nil
			}
			return rows[0], nil
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ator admin: leitura funciona.
	res, err := c.Eval(ctx, EvalRequest{
		Kind:         KindExpr,
		Code:         "await callHost('db.read', {})",
		Capabilities: []Capability{CapDBRead},
	}, map[Capability]CallbackFunc{CapDBRead: dbReadCallback(identity.RoleAdmin)})
	if err != nil {
		t.Fatalf("Eval() (admin) erro inesperado: %v", err)
	}
	if !res.OK {
		t.Fatalf("res.OK = false (admin), erro = %+v", res.Error)
	}
	m, _ := res.Result.(map[string]any)
	if m["title"] != "Dune" {
		t.Errorf("res.Result = %+v, esperado title=Dune", res.Result)
	}

	// Ator público: a tabela "books" (metadata.TableOptions{} zero-value)
	// é de LEITURA PÚBLICA por padrão — então esta chamada também
	// funciona, provando que a MESMA regra de GO-015 (não uma checagem
	// nova) se aplica ao callback: se a tabela fosse admin-only, o
	// público receberia ErrUnauthorized de records.Rows, propagado como
	// erro de runtime, exatamente como qualquer outra leitura no backend.
	res2, err2 := c.Eval(ctx, EvalRequest{
		Kind:         KindExpr,
		Code:         "await callHost('db.read', {})",
		Capabilities: []Capability{CapDBRead},
	}, map[Capability]CallbackFunc{CapDBRead: dbReadCallback(identity.RolePublic)})
	if err2 != nil {
		t.Fatalf("Eval() (público) erro inesperado: %v", err2)
	}
	if !res2.OK {
		t.Fatalf("res2.OK = false (público, tabela de leitura pública), erro = %+v", res2.Error)
	}

	// Terceiro caso: tabela ADMIN-ONLY — a MESMA checagem de GO-015 agora
	// nega o ator público, propagada pelo callback como erro de runtime.
	// Prova que o callback não contorna records.Rows de forma nenhuma.
	if err := db.WithTenant(context.Background(), tenant, func(ctx context.Context, tx pgx.Tx) error {
		secret, err := metadata.CreateTable(ctx, tx, identity.RoleAdmin, "secrets", metadata.TableOptions{MinRoleRead: identity.RoleAdmin})
		if err != nil {
			return err
		}
		if _, err := metadata.AddField(ctx, tx, identity.RoleAdmin, secret.ID, metadata.FieldDef{Name: "title", Type: metadata.FieldText}); err != nil {
			return err
		}
		_, err = records.CreateRecord(ctx, tx, identity.RoleAdmin, "secrets", map[string]any{"title": "classificado"}, nil)
		return err
	}); err != nil {
		t.Fatalf("criar tabela admin-only: %v", err)
	}

	secretCallback := func(ctx context.Context, args map[string]any) (any, error) {
		var rows []map[string]any
		err := db.WithTenant(ctx, tenant, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			rows, err = records.Rows(ctx, tx, identity.RolePublic, records.Query{Table: "secrets", Limit: 1})
			return err
		})
		return rows, err
	}
	res3, err3 := c.Eval(ctx, EvalRequest{
		Kind:         KindExpr,
		Code:         "await callHost('db.read', {})",
		Capabilities: []Capability{CapDBRead},
	}, map[Capability]CallbackFunc{CapDBRead: secretCallback})
	// Uma falha reportada pelo host (aqui, a autorização negada dentro do
	// callback) sempre volta como (EvalResult{OK:false}, erro não-nil) —
	// mesmo contrato de qualquer chamada com ok=false, ver Client.Eval.
	if !errors.Is(err3, ErrRuntime) {
		t.Fatalf("Eval() (público, tabela admin-only): err = %v, esperado ErrRuntime (autorização negada propagada do callback)", err3)
	}
	if res3.OK {
		t.Fatal("res3.OK = true (público, tabela admin-only) — o callback contornou a autorização de records.Rows")
	}
}

func TestEval_CapabilityDenied_NotDeclared(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	called := false
	_, err := c.Eval(ctx, EvalRequest{
		Kind:         KindExpr,
		Code:         "await callHost('db.read', {})",
		Capabilities: nil, // não concedida
	}, map[Capability]CallbackFunc{
		CapDBRead: func(ctx context.Context, args map[string]any) (any, error) {
			called = true
			return nil, nil
		},
	})
	if !errors.Is(err, ErrCapabilityDenied) {
		t.Fatalf("err = %v, esperado ErrCapabilityDenied", err)
	}
	if called {
		t.Error("o callback foi invocado apesar de a capacidade não ter sido concedida — negação não está contida")
	}
}

// TestEval_Timeout_IsContained é a prova direta do critério de aceite
// "timeout... é contido": uma chamada com laço síncrono infinito estoura
// o timeout do contexto, e o Client MATA o processo preso — a PRÓXIMA
// chamada, com um Client novo, ainda funciona (prova que o mecanismo de
// respawn realmente sobe um host novo, não fica com um processo morto
// pendurado).
func TestEval_Timeout_IsContained(t *testing.T) {
	c := newTestClient(t)

	shortCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := c.Eval(shortCtx, EvalRequest{
		Kind:         KindExpr,
		Code:         "(() => { while (true) {} })()",
		Capabilities: nil,
		TimeoutMs:    60000, // maior que o timeout do ctx Go — o Client, não o vm.Script, precisa conter isto
	}, nil)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, esperado ErrTimeout", err)
	}

	// Contido de verdade: a MESMA instância de Client continua utilizável.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	res, err2 := c.Eval(ctx2, EvalRequest{Kind: KindExpr, Code: "1+1", Capabilities: nil}, nil)
	if err2 != nil {
		t.Fatalf("Eval() após timeout contido: erro inesperado: %v", err2)
	}
	if !res.OK || res.Result != float64(2) {
		t.Fatalf("Eval() após timeout contido: res = %+v, esperado OK com result=2", res)
	}
}

// TestEval_Crash_IsContained mata o processo do host EXTERNAMENTE
// (simula qualquer causa real de crash — OOM, sinal, bug do V8) enquanto
// uma chamada está em voo, e confirma que (a) a chamada em voo recebe
// ErrCrashed, nunca trava, e (b) a chamada seguinte sobe um host novo e
// funciona normalmente.
func TestEval_Crash_IsContained(t *testing.T) {
	c := newTestClient(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Garante que o processo já subiu, para poder matá-lo por baixo do Client.
	if _, err := c.Eval(ctx, EvalRequest{Kind: KindExpr, Code: "1", Capabilities: nil}, nil); err != nil {
		t.Fatalf("aquecimento: %v", err)
	}

	c.mu.Lock()
	proc := c.proc
	c.mu.Unlock()
	if proc == nil || proc.Process == nil {
		t.Fatal("processo do host não está rodando após o aquecimento")
	}
	if err := proc.Process.Kill(); err != nil {
		t.Fatalf("matar processo do host: %v", err)
	}

	_, err := c.Eval(ctx, EvalRequest{Kind: KindExpr, Code: "1+1", Capabilities: nil}, nil)
	if !errors.Is(err, ErrCrashed) {
		t.Fatalf("err = %v, esperado ErrCrashed (processo morto por baixo do Client)", err)
	}

	// Contido de verdade: a próxima chamada sobe um host novo sozinha.
	res, err2 := c.Eval(ctx, EvalRequest{Kind: KindExpr, Code: "1+1", Capabilities: nil}, nil)
	if err2 != nil {
		t.Fatalf("Eval() após crash contido: erro inesperado: %v", err2)
	}
	if !res.OK || res.Result != float64(2) {
		t.Fatalf("Eval() após crash contido: res = %+v, esperado OK com result=2", res)
	}
}

func TestEval_RuntimeError_DoesNotKillProcess(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.Eval(ctx, EvalRequest{Kind: KindExpr, Code: "row.nope.explode", Capabilities: nil}, nil)
	if err == nil || res.OK {
		t.Fatal("esperado falha de runtime_error")
	}
	if !errors.Is(err, ErrRuntime) {
		t.Fatalf("err = %v, esperado ErrRuntime", err)
	}

	c.mu.Lock()
	stillSameProc := c.proc != nil
	c.mu.Unlock()
	if !stillSameProc {
		t.Error("um erro de runtime comum derrubou o processo do host — deveria continuar vivo (só timeout/crash derrubam)")
	}
}

// TestEval_UnsupportedReference_DoesNotKillProcess prova o lado Go do
// achado de GO-004 caso #6 fechado em GO-023: referenciar um singleton de
// domínio (Table/File/View) sem canal de callback explícito nunca vira um
// `nil`/resultado manso — o host lança um erro distinto e explícito
// (unsupported_reference), propagado aqui como ErrUnsupportedReference,
// nunca confundido com ErrRuntime (a fórmula pode estar sintaticamente
// perfeita; é a CLASSE de recurso que não é suportada). Como qualquer
// outro erro que não seja crash/timeout, o processo do host continua vivo.
func TestEval_UnsupportedReference_DoesNotKillProcess(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := c.Eval(ctx, EvalRequest{Kind: KindExpr, Code: "Table.findOne({id: row.id})", Context: EvalContext{Row: map[string]any{"id": 1}}, Capabilities: nil}, nil)
	if err == nil || res.OK {
		t.Fatal("esperado falha de unsupported_reference")
	}
	if !errors.Is(err, ErrUnsupportedReference) {
		t.Fatalf("err = %v, esperado ErrUnsupportedReference", err)
	}
	if errors.Is(err, ErrRuntime) {
		t.Fatal("unsupported_reference não deveria ser classificado como ErrRuntime — são classes de falha distintas")
	}

	c.mu.Lock()
	stillSameProc := c.proc != nil
	c.mu.Unlock()
	if !stillSameProc {
		t.Error("unsupported_reference derrubou o processo do host — deveria continuar vivo (só timeout/crash derrubam)")
	}
}

// TestEval_MemoryLimit_CrashIsContained é a prova mais direta da fronteira
// de segurança que ADR-0005 realmente exige: não a VM (vm.Script não tem
// como limitar memória), mas o PROCESSO — `--max-old-space-size` faz o
// Node inteiro morrer de OOM se um plugin tentar alocar mais memória do
// que o permitido, e o Client precisa conter isso exatamente como
// qualquer outro crash.
func TestEval_MemoryLimit_CrashIsContained(t *testing.T) {
	c := NewClient(ClientOptions{NodeBin: "node", HostScript: hostScriptPath(t), MaxOldSpaceSizeMB: 32})
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Aloca repetidamente arrays grandes até estourar o heap de 32MB —
	// bem menor que o padrão do Node (o processo INTEIRO morre, não só a
	// chamada).
	_, err := c.Eval(ctx, EvalRequest{
		Kind:         KindExpr,
		Code:         "(() => { const chunks = []; while (true) { chunks.push(new Array(1e7).fill(0)); } })()",
		Capabilities: nil,
	}, nil)
	if err == nil {
		t.Fatal("esperado erro (crash por OOM ou timeout do laço síncrono) — a chamada não deveria ter sucesso")
	}
	if !errors.Is(err, ErrCrashed) && !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, esperado ErrCrashed (OOM) ou ErrTimeout (vm.Script interrompeu antes do OOM)", err)
	}

	// Contido de verdade: a próxima chamada sobe um host novo, com o
	// mesmo limite de memória, e funciona para uma alocação pequena.
	res, err2 := c.Eval(ctx, EvalRequest{Kind: KindExpr, Code: "1+1", Capabilities: nil}, nil)
	if err2 != nil {
		t.Fatalf("Eval() após limite de memória contido: erro inesperado: %v", err2)
	}
	if !res.OK || res.Result != float64(2) {
		t.Fatalf("Eval() após limite de memória contido: res = %+v, esperado OK com result=2", res)
	}
}
