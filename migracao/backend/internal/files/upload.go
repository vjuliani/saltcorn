package files

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
)

// newStorageKeyFunc indireciona NewStorageKey — permite que um teste force
// uma chave conhecida (ex.: para provocar uma colisão de storage_key e
// provar que Upload limpa o arquivo físico quando CreateFile falha),
// mesmo padrão idiomático de `var now = time.Now` para relógios
// testáveis. Nunca sobrescrito fora de teste.
var newStorageKeyFunc = NewStorageKey

// Upload grava o conteúdo de r no backend físico e registra o catálogo
// dentro de tx — nesta ordem, nunca a inversa: Save primeiro (fora de
// qualquer transação SQL), CreateFile depois. Se CreateFile falhar, o
// arquivo físico já salvo é removido IMEDIATAMENTE (Backend.Delete),
// nunca esperando uma varredura posterior — só uma queda abrupta do
// processo entre Save e CreateFile (que nenhum defer intercepta) deixa
// margem para os dois ficarem temporariamente inconsistentes, e mesmo
// nesse caso o arquivo físico continua um staging válido de
// CleanupOrphans até o limite de idade configurado.
//
// LIMITAÇÃO DELIBERADA (documentada, não esquecida): se tx commitar com
// sucesso aqui, mas o CHAMADOR fizer rollback da transação mais tarde por
// um motivo não relacionado a este upload (outro hook na mesma
// transação falhando depois), o arquivo físico já finalizado por Save
// fica órfão sem que CleanupOrphans o detecte (ele só varre nomes de
// STAGING, e o arquivo já foi promovido ao nome final por Save). Cobrir
// esse caso exigiria um protocolo de duas fases (finalizar só DEPOIS do
// commit externo confirmado) — fora do subconjunto prioritário desta
// tarefa, que se concentra em "upload interrompido" (Save falhando),
// não em "transação externa abortada por outro motivo depois do
// upload ter sucedido".
func Upload(ctx context.Context, tx pgx.Tx, backend Backend, meta File, r io.Reader) (File, error) {
	key := newStorageKeyFunc()
	size, err := backend.Save(ctx, key, r)
	if err != nil {
		return File{}, err
	}
	meta.StorageKey = key
	meta.SizeBytes = size

	created, err := CreateFile(ctx, tx, meta)
	if err != nil {
		_ = backend.Delete(ctx, key)
		return File{}, err
	}
	return created, nil
}

// Download resolve o catálogo, confere autorização (CanRead) ANTES de
// tocar o backend físico, e só então abre os bytes — "usuário sem acesso
// não baixa arquivo" (critério de aceite) é estrutural: ErrNotAuthorized
// nunca chega a chamar Backend.Open.
func Download(ctx context.Context, tx pgx.Tx, backend Backend, actorRole identity.RoleID, actorUserID *int, fileID int) (io.ReadCloser, File, error) {
	f, err := GetFile(ctx, tx, fileID)
	if err != nil {
		return nil, File{}, err
	}
	if !CanRead(actorRole, actorUserID, f) {
		return nil, File{}, ErrNotAuthorized
	}
	r, err := backend.Open(ctx, f.StorageKey)
	if err != nil {
		return nil, File{}, err
	}
	return r, f, nil
}
