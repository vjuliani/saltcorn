package lease

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrLeaseHeld é devolvido por Acquire quando outro owner detém um lease
// ainda válido (não expirado) para name — o chamador deve tratar isto como
// "outro worker já está cuidando deste job agora", nunca tentar contornar.
var ErrLeaseHeld = errors.New("lease: já detido por outro proprietário")

// Acquire reivindica (ou renova) o lease name por ttl, atomicamente: um
// único UPSERT condicional decide tudo dentro do Postgres, sem uma
// leitura seguida de escrita separada (que teria uma janela de corrida
// entre duas chamadas concorrentes). A reivindicação sucede se e somente
// se:
//   - o lease ainda não existe, OU
//   - o lease existente já expirou (expires_at < now()), OU
//   - ownerID já é o dono atual (renovação — o mesmo worker estendendo seu
//     próprio lease antes que expire, para um job de longa duração).
//
// Caso contrário (outro owner detém um lease ainda válido), devolve
// ErrLeaseHeld sem gravar nada — nunca "quase reivindica".
func Acquire(ctx context.Context, tx pgx.Tx, name, ownerID string, ttl time.Duration) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO _sc_leases (name, owner_id, acquired_at, expires_at)
		VALUES ($1, $2, now(), now() + make_interval(secs => $3))
		ON CONFLICT (name) DO UPDATE
			SET owner_id = EXCLUDED.owner_id, acquired_at = now(), expires_at = EXCLUDED.expires_at
			WHERE _sc_leases.owner_id = EXCLUDED.owner_id OR _sc_leases.expires_at < now()
	`, name, ownerID, ttl.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseHeld
	}
	return nil
}

// Release libera o lease name, mas SÓ se ownerID ainda é o dono corrente —
// nunca libera o lease de outro owner. Protege contra um worker "atrasado"
// (ex.: pausado pelo GC, ou uma chamada de rede lenta) que só executa seu
// Release depois de já ter perdido o lease por expiração e reaquisição por
// outro worker: sem essa checagem, esse Release atrasado apagaria
// silenciosamente o lease do NOVO dono legítimo.
func Release(ctx context.Context, tx pgx.Tx, name, ownerID string) error {
	_, err := tx.Exec(ctx, `DELETE FROM _sc_leases WHERE name = $1 AND owner_id = $2`, name, ownerID)
	return err
}

// HeldBy devolve o owner_id corrente de name, ou "" se o lease não existe
// OU já expirou (um lease expirado não tem dono para fins de leitura,
// mesmo que a linha ainda exista fisicamente até a próxima Acquire a
// sobrescrever) — uso principal: testes e diagnóstico.
func HeldBy(ctx context.Context, tx pgx.Tx, name string) (string, error) {
	var ownerID string
	err := tx.QueryRow(ctx, `SELECT owner_id FROM _sc_leases WHERE name = $1 AND expires_at >= now()`, name).Scan(&ownerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return ownerID, nil
}
