// Corpus de expressões prioritárias exigido pelo critério de aceite de
// GO-023: "corpus cobre coerção, null, datas, decimal, erros e async;
// expressão desconhecida nunca muda resultado silenciosamente." Sobe o
// host REAL compilado (migracao/packages/pluginhost/dist/src/host.js) e
// Postgres real — mesma convenção de internal/pluginhost/client_test.go.
package expression

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/metadata"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/cutover"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/pluginhost"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/types"
)

// TestEval_LegacyOwner_NeverTouchesHost prova o critério de aceite "plugin
// transacional incompatível mantém operação integral no legado" pelo lado
// deste pacote: um tenant cuja capacidade plugins.expr NUNCA foi trocada
// para OwnerGo (guard "frio", sem SwitchOwner) recebe ErrLegacyOwner sem
// que o Client sequer tente subir o processo do host — usa um HostScript
// inexistente de propósito: se Eval chegasse a chamar pluginhost.Client.Eval,
// a chamada falharia ao tentar iniciar o processo, não com ErrLegacyOwner.
func TestEval_LegacyOwner_NeverTouchesHost(t *testing.T) {
	client := pluginhost.NewClient(pluginhost.ClientOptions{NodeBin: "node", HostScript: "/caminho/que/nao/existe/host.js"})
	t.Cleanup(func() { _ = client.Close() })

	ev := &Evaluator{Client: client, Guard: cutover.NewGuard()}
	tenant := tenancy.Tenant("never_switched")

	_, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "1+1",
		ExpectedType: metadata.FieldInteger,
	}, nil)
	if !errors.Is(err, ErrLegacyOwner) {
		t.Fatalf("err = %v, esperado ErrLegacyOwner", err)
	}
}

func TestEval_Sync_Arithmetic_DecimalCorpus(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	got, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "row.price * row.qty",
		Row:          map[string]any{"price": 19.9, "qty": 3},
		ExpectedType: metadata.FieldFloat,
	}, nil)
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	f, ok := got.(float64)
	if !ok || !types.FloatEquals(f, 59.7, 2) {
		t.Fatalf("Eval() = %v (%T), esperado ~59.7 (tolerância decimal, sem tipo Decimal dedicado)", got, got)
	}
}

func TestEval_NullCoalescing_Text(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	got, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "row.nome ?? 'padrão'",
		Row:          map[string]any{},
		ExpectedType: metadata.FieldText,
	}, nil)
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	if got != "padrão" {
		t.Fatalf("Eval() = %v, esperado \"padrão\"", got)
	}
}

func TestEval_Boolean(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	got, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "row.idade >= 18",
		Row:          map[string]any{"idade": 20},
		ExpectedType: metadata.FieldBoolean,
	}, nil)
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	if got != true {
		t.Fatalf("Eval() = %v, esperado true", got)
	}
}

// TestEval_Date_RoundTripsAsRFC3339String documenta, sem "corrigir"
// silenciosamente, a mesma limitação já conhecida do legado (models/
// expression.ts, postProcessVmResult) e confirmada experimentalmente por
// GO-004: um Date criado dentro da expressão atravessa a fronteira JSON e
// chega como STRING (ISO 8601 com milissegundos), nunca como um tipo Date
// nativo. internal/types.CoerceDate reconhece esse formato (RFC3339)
// explicitamente — não é um bug desta tarefa, é o comportamento esperado
// e testado.
func TestEval_Date_RoundTripsAsRFC3339String(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	got, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "new Date(row.ts)",
		Row:          map[string]any{"ts": "2026-01-15T10:30:00Z"},
		ExpectedType: metadata.FieldDate,
	}, nil)
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	when, ok := got.(time.Time)
	if !ok {
		t.Fatalf("Eval() = %v (%T), esperado time.Time (coagido da string ISO que o round-trip JSON produz)", got, got)
	}
	want, _ := time.Parse(time.RFC3339, "2026-01-15T10:30:00Z")
	if !when.Equal(want) {
		t.Fatalf("Eval() = %v, esperado %v", when, want)
	}
}

// TestEval_Async_DBReadCallback é o caso "async" do corpus — reaproveita o
// MESMO callback de leitura (db.read) e a MESMA autorização por papel já
// provados em internal/pluginhost/client_test.go, agora atravessando a
// camada de coerção de tipo deste pacote.
func TestEval_Async_DBReadCallback(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	got, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "(await callHost('db.read', {})).title",
		Capabilities: []pluginhost.Capability{pluginhost.CapDBRead},
		ExpectedType: metadata.FieldText,
	}, map[pluginhost.Capability]pluginhost.CallbackFunc{
		pluginhost.CapDBRead: dbReadCallback(db, tenant, identity.RoleAdmin),
	})
	if err != nil {
		t.Fatalf("Eval() erro inesperado: %v", err)
	}
	if got != "Dune" {
		t.Fatalf("Eval() = %v, esperado \"Dune\"", got)
	}
}

func TestEval_RuntimeError_Propagates(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	_, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "row.nope.explode",
		ExpectedType: metadata.FieldText,
	}, nil)
	if !errors.Is(err, pluginhost.ErrRuntime) {
		t.Fatalf("err = %v, esperado ErrRuntime", err)
	}
}

// TestEval_ClosureSerialization_Fails prova, no nível desta fachada, o
// achado de GO-004 caso #9: uma função cujo corpo depende de uma variável
// do ambiente de ORIGEM (nunca enviada ao host) falha com um erro de
// runtime explícito — nenhuma fronteira de processo transporta ambiente
// léxico, e este pacote não tenta contornar isso de forma nenhuma.
func TestEval_ClosureSerialization_Fails(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	_, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "(function(){ return outerCounter + 1; })()",
		ExpectedType: metadata.FieldInteger,
	}, nil)
	if !errors.Is(err, pluginhost.ErrRuntime) {
		t.Fatalf("err = %v, esperado ErrRuntime (closure não serializável)", err)
	}
}

// TestEval_UnsupportedReference_SingletonDomain prova, no nível desta
// fachada, o achado de GO-004 caso #6 fechado por GO-023: referenciar um
// singleton de domínio nunca produz um resultado manso — chega aqui como
// um erro distinto e explícito, nunca confundido com ErrAmbiguousResult
// ou ErrRuntime.
func TestEval_UnsupportedReference_SingletonDomain(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	_, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "Table.findOne({id: row.id})",
		Row:          map[string]any{"id": 1},
		ExpectedType: metadata.FieldBoolean,
	}, nil)
	if !errors.Is(err, pluginhost.ErrUnsupportedReference) {
		t.Fatalf("err = %v, esperado ErrUnsupportedReference", err)
	}
}

// TestEval_AmbiguousResult_NeverSilentlyWrong prova o outro lado do mesmo
// critério de aceite: um resultado sintaticamente válido mas de FORMATO
// incompatível com ExpectedType nunca é aceito silenciosamente (nem
// truncado, nem convertido "na marra") — vira ErrAmbiguousResult.
func TestEval_AmbiguousResult_NeverSilentlyWrong(t *testing.T) {
	db := testDB(t)
	tenant, guard := testFixture(t, db)
	ev := &Evaluator{Client: newTestClient(t), Guard: guard}

	_, err := ev.Eval(context.Background(), tenant, Request{
		Code:         "({foo: 1})",
		ExpectedType: metadata.FieldInteger,
	}, nil)
	if !errors.Is(err, ErrAmbiguousResult) {
		t.Fatalf("err = %v, esperado ErrAmbiguousResult (objeto não é coagível para integer)", err)
	}
}
