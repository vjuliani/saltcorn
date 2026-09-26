package triggers

import (
	"context"
	"encoding/json"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
)

// WhenTrigger é o evento que dispara um Trigger — os quatro eventos
// diretamente ligados aos comandos de registro de GO-013
// (WhenValidate/Insert/Update/Delete, sempre com TableID != 0), mais
// (GO-052) um NOME DE EVENTO livre para um trigger de evento nomeado
// (sempre com TableID == 0) — o subconjunto prioritário de `when_trigger`
// do legado (models/trigger.ts), que é uma coluna texto livre sem enum no
// banco; a UI do legado só restringe a CRIAÇÃO a uma lista fixa
// (Trigger.when_options), nunca a validação em si. Schedule/Cron/API
// call/Login/PageLoad como EMISSÕES AUTOMÁTICAS do próprio servidor (o
// servidor chamando EmitEvent sozinho em cada login/carregamento de
// página) continuam fora de escopo (GO-025 cobre agendamento via rotas
// próprias; os demais não têm um ponto de emissão automática no servidor
// Go ainda) — só o MECANISMO genérico de emissão via HTTP é portado aqui.
type WhenTrigger string

const (
	// WhenValidate roda ANTES da escrita física, dentro da mesma transação
	// — pode abortar a operação inteira (equivalente a BeforeInsert/Update/
	// Delete de internal/records.Hooks).
	WhenValidate WhenTrigger = "Validate"
	// WhenInsert/WhenUpdate/WhenDelete rodam DEPOIS da escrita física,
	// ainda na mesma transação (a menos que Trigger.AfterCommit) —
	// equivalente a AfterInsert/Update/Delete.
	WhenInsert WhenTrigger = "Insert"
	WhenUpdate WhenTrigger = "Update"
	WhenDelete WhenTrigger = "Delete"
)

// tableBoundWhens são os únicos valores válidos quando TableID != 0 —
// nunca um nome de evento arbitrário disfarçado de evento de registro
// (evitaria, por exemplo, um trigger com TableID setado e
// When="ReceiveMobileShareData", que TriggersFor nunca encontraria e
// EmitEvent também nunca encontraria — um trigger morto, silenciosamente
// nunca disparado).
func (w WhenTrigger) tableBound() bool {
	switch w {
	case WhenValidate, WhenInsert, WhenUpdate, WhenDelete:
		return true
	default:
		return false
	}
}

// valid(tableID) — a validação de CreateTrigger depende de TableID:
// TableID != 0 exige um dos 4 valores ligados a registro; TableID == 0
// (evento nomeado, GO-052) exige um nome NÃO VAZIO que não seja um dos 4
// reservados (mesma razão do comentário de tableBound — um nome
// reservado com TableID == 0 também nunca seria encontrado por nenhuma
// das duas consultas de despacho).
func (w WhenTrigger) valid(tableID int) bool {
	if tableID != 0 {
		return w.tableBound()
	}
	return w != "" && !w.tableBound()
}

// Trigger é uma entrada do catálogo _sc_triggers — o equivalente reduzido
// do Trigger do legado (models/trigger.ts) para o subconjunto desta
// tarefa: sem `channel`/`min_role`/tipos de evento fora dos cobertos
// acima, sem inventário de ações de terceiro (ADR-0005, ver
// Dispatcher.Actions).
type Trigger struct {
	ID int
	// TableID == 0 (GO-052) significa "trigger de evento nomeado" — sem
	// tabela, encontrado por TriggersForEvent, nunca por TriggersFor
	// (que exige um id de tabela real, sempre != 0 para uma tabela
	// existente — mesma convenção de "zero value Go = ausente" já usada
	// em record_id==0 para "registro novo").
	TableID int
	When    WhenTrigger
	Action  string
	// OnlyIf é uma fórmula JS opcional (equivalente a `configuration._only_if`
	// do legado) avaliada via internal/expression contra a linha e o ator —
	// "" significa "sempre dispara", nunca avaliado (sem tocar no host).
	OnlyIf string
	// AfterCommit espelha `configuration._after_commit` do legado — ver
	// Dispatcher.enqueueAfterCommit para a diferença deliberada de garantia.
	AfterCommit bool
	// Configuration (GO-029) são os parâmetros PRÓPRIOS desta instância de
	// trigger, passados para a ActionFunc no disparo (ex.: `to`/`subject`/
	// `body` de um `send_email`, `url` de um `webhook`) — ver actions.go.
	// nil/vazio é válido para ações que não precisam de parâmetro (ex.:
	// ações de teste que ignoram config).
	Configuration map[string]any
}

// CreateTrigger insere uma nova entrada no catálogo. Não valida se
// Action está registrada em nenhum Dispatcher.Actions — essa checagem só
// faz sentido no momento do disparo (um Dispatcher pode registrar ações
// diferentes em contextos diferentes, ex.: testes vs. produção).
//
// GO-052: um trigger de evento nomeado (TableID == 0) nunca é
// AfterCommit — esse mecanismo enfileira via `outbox.Do` com uma chave
// derivada de `record["id"]` (ver Dispatcher.enqueueAfterCommit), um
// conceito que só faz sentido para um trigger amarrado à escrita de UM
// registro. Rejeitado explicitamente aqui, na criação, em vez de uma
// divergência silenciosa dentro de EmitEvent (que sempre despacha
// evento nomeado de forma síncrona, ignorando AfterCommit se ele
// escapasse desta checagem).
func CreateTriggerTx(ctx context.Context, tx database.Tx, t Trigger) (Trigger, error) {
	if !t.When.valid(t.TableID) {
		return Trigger{}, ErrInvalidWhenTrigger
	}
	if t.TableID == 0 && t.AfterCommit {
		return Trigger{}, ErrAfterCommitRequiresTable
	}
	var onlyIf *string
	if t.OnlyIf != "" {
		onlyIf = &t.OnlyIf
	}
	config := t.Configuration
	if config == nil {
		config = map[string]any{}
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return Trigger{}, err
	}
	var tableID *int
	if t.TableID != 0 {
		tableID = &t.TableID
	}
	err = tx.QueryRow(ctx,
		`INSERT INTO _sc_triggers (table_id, when_trigger, action, only_if, after_commit, configuration) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		tableID, string(t.When), t.Action, onlyIf, t.AfterCommit, configJSON,
	).Scan(&t.ID)
	if err != nil {
		return Trigger{}, err
	}
	t.Configuration = config
	return t, nil
}

// TriggersFor lê, em ordem de criação, os triggers registrados para
// tableID+when — a consulta que Dispatcher faz a cada escrita.
func TriggersForTx(ctx context.Context, tx database.Tx, tableID int, when WhenTrigger) ([]Trigger, error) {
	return queryTriggersTx(ctx, tx, `SELECT id, table_id, when_trigger, action, only_if, after_commit, configuration FROM _sc_triggers WHERE table_id = $1 AND when_trigger = $2 ORDER BY id`, tableID, string(when))
}

// TriggersForEvent (GO-052) lê, em ordem de criação, os triggers de
// EVENTO NOMEADO (TableID == 0) registrados para eventName — a consulta
// que Dispatcher.EmitEvent faz a cada emissão via
// `POST .../events/{eventname}`. Nunca encontra um trigger ligado a
// tabela (table_id IS NULL exclui exatamente os 4 whens de registro),
// mesma separação que CreateTrigger já impõe na escrita.
func TriggersForEventTx(ctx context.Context, tx database.Tx, eventName string) ([]Trigger, error) {
	return queryTriggersTx(ctx, tx, `SELECT id, table_id, when_trigger, action, only_if, after_commit, configuration FROM _sc_triggers WHERE table_id IS NULL AND when_trigger = $1 ORDER BY id`, eventName)
}

// ListAll lê TODOS os triggers do tenant, em ordem de criação — usado por
// internal/pack (GO-027) para exportar a aplicação inteira; nenhum outro
// chamador precisava de uma visão não filtrada por tabela+evento até
// aqui.
func ListAllTx(ctx context.Context, tx database.Tx) ([]Trigger, error) {
	return queryTriggersTx(ctx, tx, `SELECT id, table_id, when_trigger, action, only_if, after_commit, configuration FROM _sc_triggers ORDER BY id`)
}

func queryTriggersTx(ctx context.Context, tx database.Tx, sql string, args ...any) ([]Trigger, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Trigger
	for rows.Next() {
		var t Trigger
		var tableID *int
		var whenStr string
		var onlyIf *string
		var configJSON []byte
		if err := rows.Scan(&t.ID, &tableID, &whenStr, &t.Action, &onlyIf, &t.AfterCommit, &configJSON); err != nil {
			return nil, err
		}
		if tableID != nil {
			t.TableID = *tableID
		}
		t.When = WhenTrigger(whenStr)
		if onlyIf != nil {
			t.OnlyIf = *onlyIf
		}
		if len(configJSON) > 0 {
			if err := json.Unmarshal(configJSON, &t.Configuration); err != nil {
				return nil, err
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
