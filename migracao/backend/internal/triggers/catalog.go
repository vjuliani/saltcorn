package triggers

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// WhenTrigger é o evento que dispara um Trigger — subconjunto prioritário
// do `when_trigger` do legado (models/trigger.ts): os quatro eventos
// diretamente ligados aos comandos de registro de GO-013. Schedule/Cron/
// API call/Login/etc. do legado ficam fora de escopo (GO-025 cobre
// agendamento; os demais não têm um comando Go equivalente ainda).
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

func (w WhenTrigger) valid() bool {
	switch w {
	case WhenValidate, WhenInsert, WhenUpdate, WhenDelete:
		return true
	default:
		return false
	}
}

// Trigger é uma entrada do catálogo _sc_triggers — o equivalente reduzido
// do Trigger do legado (models/trigger.ts) para o subconjunto desta
// tarefa: sem `channel`/`min_role`/tipos de evento fora dos 4 acima, sem
// inventário de ações de terceiro (ADR-0005, ver Dispatcher.Actions).
type Trigger struct {
	ID      int
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
func CreateTrigger(ctx context.Context, tx pgx.Tx, t Trigger) (Trigger, error) {
	if !t.When.valid() {
		return Trigger{}, ErrInvalidWhenTrigger
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
	err = tx.QueryRow(ctx,
		`INSERT INTO _sc_triggers (table_id, when_trigger, action, only_if, after_commit, configuration) VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		t.TableID, string(t.When), t.Action, onlyIf, t.AfterCommit, configJSON,
	).Scan(&t.ID)
	if err != nil {
		return Trigger{}, err
	}
	t.Configuration = config
	return t, nil
}

// TriggersFor lê, em ordem de criação, os triggers registrados para
// tableID+when — a consulta que Dispatcher faz a cada escrita.
func TriggersFor(ctx context.Context, tx pgx.Tx, tableID int, when WhenTrigger) ([]Trigger, error) {
	return queryTriggers(ctx, tx, `SELECT id, table_id, when_trigger, action, only_if, after_commit, configuration FROM _sc_triggers WHERE table_id = $1 AND when_trigger = $2 ORDER BY id`, tableID, string(when))
}

// ListAll lê TODOS os triggers do tenant, em ordem de criação — usado por
// internal/pack (GO-027) para exportar a aplicação inteira; nenhum outro
// chamador precisava de uma visão não filtrada por tabela+evento até
// aqui.
func ListAll(ctx context.Context, tx pgx.Tx) ([]Trigger, error) {
	return queryTriggers(ctx, tx, `SELECT id, table_id, when_trigger, action, only_if, after_commit, configuration FROM _sc_triggers ORDER BY id`)
}

func queryTriggers(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]Trigger, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Trigger
	for rows.Next() {
		var t Trigger
		var whenStr string
		var onlyIf *string
		var configJSON []byte
		if err := rows.Scan(&t.ID, &t.TableID, &whenStr, &t.Action, &onlyIf, &t.AfterCommit, &configJSON); err != nil {
			return nil, err
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
