// Rotas de arquivo (GO-051) — o consumidor HTTP que faltava para
// internal/files (GO-026): a própria GO-026 documentou a ausência como
// decisão de escopo explícita ("nenhuma rota HTTP... um consumidor HTTP
// real fica para uma tarefa futura dedicada"). GO-051 é essa tarefa,
// necessária para a fieldview "upload" do Edit (metadata.FieldFile) ter
// como funcionar de ponta a ponta: o fluxo é upload PRIMEIRO (devolve um
// id de arquivo), submissão do formulário Edit DEPOIS (usando esse id
// como valor do campo) — não um upload multipart embutido no próprio
// submitView, que continua só JSON.
package main

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/files"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
)

// uploadMaxBytes limita o tamanho de um upload — generoso para o piloto
// (a foto de um processo de guitars), nunca ilimitado (mesmo espírito de
// io.LimitReader em qualquer outro corpo de requisição desta pilha).
const uploadMaxBytes = 20 << 20 // 20 MiB

type fileResponse struct {
	ID        int    `json:"id"`
	Filename  string `json:"filename"`
	MimeSuper string `json:"mime_super"`
	MimeSub   string `json:"mime_sub"`
	SizeBytes int64  `json:"size_bytes"`
}

func fileToResponse(f files.File) fileResponse {
	return fileResponse{ID: f.ID, Filename: f.Filename, MimeSuper: f.MimeSuper, MimeSub: f.MimeSub, SizeBytes: f.SizeBytes}
}

// splitMime resolve super/sub a partir do nome do arquivo (extensão) —
// mesmo espírito de mime.TypeByExtension já usado pela árvore padrão do
// Go; "application/octet-stream" quando a extensão é desconhecida ou
// ausente, nunca um erro (um arquivo sem extensão reconhecida ainda é um
// upload válido).
func splitMime(filename string) (super, sub string) {
	ct := mime.TypeByExtension(filepath.Ext(filename))
	if ct == "" {
		return "application", "octet-stream"
	}
	// mime.TypeByExtension pode devolver parâmetros (ex.: "text/plain;
	// charset=utf-8") — só o tipo/subtipo interessa aqui.
	if idx := strings.IndexByte(ct, ';'); idx >= 0 {
		ct = ct[:idx]
	}
	parts := strings.SplitN(ct, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "application", "octet-stream"
	}
	return parts[0], parts[1]
}

// uploadFileHandler implementa POST /v1/tenants/{tenant}/files
// (multipart/form-data, campo "file") — qualquer ator autenticado pode
// enviar um arquivo (a autorização de ONDE um id de arquivo pode ser
// usado — ex.: um campo FieldFile de uma tabela — é decidida pelas
// regras normais de escrita daquela tabela/view, não aqui); o arquivo
// nasce MinRoleRead=RoleAdmin (nunca público por omissão, mesmo espírito
// de View/Table) e UserID = o próprio ator (dono).
func uploadFileHandler(tracker *shutdown.Tracker, db *database.DB, backend *files.LocalBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		if backend == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "files_unavailable", "armazenamento de arquivos não configurado nesta instância")
			return
		}

		if err := r.ParseMultipartForm(uploadMaxBytes); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_multipart", "corpo multipart inválido ou maior que o limite permitido")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "file_required", "campo multipart \"file\" é obrigatório")
			return
		}
		defer func() { _ = file.Close() }()
		mimeSuper, mimeSub := splitMime(header.Filename)

		var created any
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			user, ok := resolveActorUser(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			userID := user.ID
			f, err := files.Upload(ctx, tx, backend, files.File{
				Filename:    header.Filename,
				MimeSuper:   mimeSuper,
				MimeSub:     mimeSub,
				MinRoleRead: identity.RoleAdmin,
				UserID:      &userID,
			}, io.LimitReader(file, uploadMaxBytes+1))
			if err != nil {
				return err
			}
			created = fileToResponse(f)
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			writeAPIError(w, http.StatusBadGateway, "upload_failed", "falha ao processar o upload")
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

// downloadFileHandler implementa GET /v1/tenants/{tenant}/files/{id} —
// streama os bytes com o Content-Type do catálogo. ErrNotFound E
// ErrNotAuthorized viram 404, nunca distinguidos na resposta — mesma
// disciplina de "nunca revelar existência" já documentada no comentário
// de files.ErrNotAuthorized (uma decisão explícita da camada de
// transporte, não do pacote de domínio).
func downloadFileHandler(tracker *shutdown.Tracker, db *database.DB, backend *files.LocalBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant, _ := tenancy.TenantFromContext(r.Context())
		id, convErr := strconv.Atoi(r.PathValue("id"))
		if convErr != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_id", "id inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}
		if backend == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "files_unavailable", "armazenamento de arquivos não configurado nesta instância")
			return
		}

		var reader io.ReadCloser
		var meta files.File
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			user, ok := resolveActorUser(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			r, f, err := files.Download(ctx, tx, backend, user.RoleID, &user.ID, id)
			if err != nil {
				return err
			}
			reader = r
			meta = f
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, files.ErrNotFound) || errors.Is(err, files.ErrNotAuthorized) {
				writeAPIError(w, http.StatusNotFound, "not_found", "arquivo não encontrado")
				return
			}
			writeAPIError(w, http.StatusBadGateway, "download_failed", "falha ao processar o download")
			return
		}
		defer func() { _ = reader.Close() }()

		w.Header().Set("Content-Type", meta.MimeSuper+"/"+meta.MimeSub)
		w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(meta.Filename))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, reader)
	}
}
