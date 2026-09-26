// Web App Manifest e Web Share Target (GO-053) — porta
// `routes/notifications.ts` do legado (`GET /manifest.json`,
// `POST /notifications/share-handler`), reaproveitando DIRETAMENTE o
// mecanismo de evento nomeado que GO-052 já construiu e testou
// (`Dispatcher.EmitEvent("ReceiveMobileShareData", ...)`).
//
// Decisões de escopo explícitas:
//
//   - manifestHandler é DELIBERADAMENTE público (sem tenancy.Middleware):
//     um manifesto PWA é, por definição do próprio padrão web, buscado
//     pelo NAVEGADOR antes de qualquer login (a tela de "Adicionar à
//     tela inicial" aparece já na página de login) — nenhuma informação
//     sensível é exposta (nome do site, ícones, cores), mesmo perfil de
//     exposição que o legado sempre teve. O tenant vem diretamente do
//     path, sem identidade delegada — mesmo assim seguro contra injeção,
//     porque WithTenant sempre sanitiza o identificador do schema
//     internamente.
//   - shareHandlerHandler EXIGE identidade delegada (mesmo padrão de
//     qualquer outra escrita) — o legado também exige usuário logado
//     (role != público) antes de emitir o evento.
//   - `enctype` do `share_target` é `application/x-www-form-urlencoded`
//     (válido pela própria especificação Web Share Target para
//     compartilhamento só de texto: title/text/url) — DELIBERADAMENTE
//     não `multipart/form-data`: compartilhamento de ARQUIVO via este
//     caminho HTTP fica fora de escopo desta task (nenhum parser
//     multipart de campos de texto existe no BFF hoje, e construir um
//     junto de uma ponte de upload para internal/files mudaria o
//     tamanho desta task de P/S para M/L). O mecanismo de trigger para
//     payload com arquivos (`row.files`) já foi provado de ponta a ponta
//     em GO-052 — só o NOVO ponto de entrada HTTP para esse caso
//     específico fica pendente, não a capacidade de disparo em si.
//   - `install_progressive_web_app` (ação client-side do legado,
//     `base-plugin/actions.ts`) fica FORA de escopo: navegadores modernos
//     já oferecem o próprio prompt de instalação nativo assim que um
//     manifesto válido existe (exatamente o entregável desta task) — a
//     ação do legado é só uma conveniência para disparar esse MESMO
//     prompt nativo a partir de um botão customizado. Nenhum mecanismo de
//     "ação com efeito no navegador" (`eval_js` do legado) existe hoje em
//     Go/BFF/frontend — construir um novo mecanismo cross-cutting só para
//     esta conveniência de nicho é desproporcional ao tamanho desta task.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vjuliani/saltcorn/migracao/backend/internal/config"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/identity"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/database"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/outbox"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/shutdown"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/platform/tenancy"
	"github.com/vjuliani/saltcorn/migracao/backend/internal/triggers"
)

// receiveMobileShareDataEvent é o nome do evento (mesmo usado por
// cmd/server/events.go) que um trigger real (`receive_share_trigger` no
// pack piloto guitars) escuta para processar conteúdo compartilhado.
const receiveMobileShareDataEvent = "ReceiveMobileShareData"

// shareTargetPath é o caminho que o manifesto declara e que o BFF regista
// — precisa bater exatamente, senão o navegador nunca chama o handler
// certo.
const shareTargetPath = "/api/bff/notifications/share-handler"

type pwaManifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

type pwaShareTargetParams struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

type pwaShareTarget struct {
	Action  string               `json:"action"`
	Method  string               `json:"method"`
	Enctype string               `json:"enctype"`
	Params  pwaShareTargetParams `json:"params"`
}

type pwaManifest struct {
	Name            string            `json:"name"`
	StartURL        string            `json:"start_url"`
	Display         string            `json:"display"`
	Icons           []pwaManifestIcon `json:"icons,omitempty"`
	ThemeColor      string            `json:"theme_color,omitempty"`
	BackgroundColor string            `json:"background_color,omitempty"`
	ShareTarget     *pwaShareTarget   `json:"share_target,omitempty"`
}

func iconMimeType(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".jpg"), strings.HasSuffix(filename, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(filename, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(filename, ".webp"):
		return "image/webp"
	default:
		return "image/png"
	}
}

// iconSrc replica a regra do legado: um valor já absoluto (contém "://")
// é usado tal qual; qualquer outro é tratado como um nome de arquivo
// servido por este backend.
func iconSrc(value string) string {
	if u, err := url.Parse(value); err == nil && u.Scheme != "" {
		return value
	}
	return "/files/serve/" + value
}

// manifestHandler implementa GET .../manifest — deliberadamente sem
// tenancy.Middleware (ver comentário do arquivo). tenant vem direto do
// path.
func manifestHandler(tracker *shutdown.Tracker, db *database.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		end, err := tracker.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer end()

		tenant := r.PathValue("tenant")
		if tenant == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_tenant", "tenant inválido")
			return
		}
		if db == nil {
			writeAPIError(w, http.StatusBadGateway, "database_unavailable", "banco não configurado nesta instância")
			return
		}

		manifest := pwaManifest{StartURL: "/", Display: "browser"}
		err = db.WithTenant(r.Context(), tenancy.Tenant(tenant), func(ctx context.Context, tx pgx.Tx) error {
			if name, ok, err := config.Get(ctx, tx, "site_name"); err != nil {
				return err
			} else if ok {
				if s, ok := name.(string); ok {
					manifest.Name = s
				}
			}
			if display, ok, err := config.Get(ctx, tx, "pwa_display"); err != nil {
				return err
			} else if ok {
				if s, ok := display.(string); ok && s != "" {
					manifest.Display = s
				}
			}

			iconsFromConfig := false
			if pwaIcons, ok, err := config.Get(ctx, tx, "pwa_icons"); err != nil {
				return err
			} else if ok {
				if list, ok := pwaIcons.([]any); ok {
					for _, item := range list {
						entry, ok := item.(map[string]any)
						if !ok {
							continue
						}
						image, _ := entry["image"].(string)
						if image == "" {
							continue
						}
						size, _ := entry["size"].(string)
						if size == "" {
							size = "144"
						}
						icon := pwaManifestIcon{Src: iconSrc(image), Sizes: size + "x" + size, Type: iconMimeType(image)}
						if maskable, _ := entry["maskable"].(bool); maskable {
							icon.Purpose = "maskable"
						}
						manifest.Icons = append(manifest.Icons, icon)
						iconsFromConfig = true
					}
				}
			}
			if !iconsFromConfig {
				if logo, ok, err := config.Get(ctx, tx, "site_logo_id"); err != nil {
					return err
				} else if ok {
					if s, ok := logo.(string); ok && s != "" {
						manifest.Icons = []pwaManifestIcon{{Src: iconSrc(s), Sizes: "144x144", Type: iconMimeType(s)}}
					}
				}
			}

			if setColors, ok, err := config.Get(ctx, tx, "pwa_set_colors"); err != nil {
				return err
			} else if ok {
				if enabled, _ := setColors.(bool); enabled {
					manifest.ThemeColor = "black"
					manifest.BackgroundColor = "red"
					if theme, ok, err := config.Get(ctx, tx, "pwa_theme_color"); err != nil {
						return err
					} else if ok {
						if s, ok := theme.(string); ok && s != "" {
							manifest.ThemeColor = s
						}
					}
					if bg, ok, err := config.Get(ctx, tx, "pwa_background_color"); err != nil {
						return err
					} else if ok {
						if s, ok := bg.(string); ok && s != "" {
							manifest.BackgroundColor = s
						}
					}
				}
			}

			shareTriggers, err := triggers.TriggersForEvent(ctx, tx, receiveMobileShareDataEvent)
			if err != nil {
				return err
			}
			if len(shareTriggers) > 0 {
				manifest.ShareTarget = &pwaShareTarget{
					Action:  shareTargetPath,
					Method:  "POST",
					Enctype: "application/x-www-form-urlencoded",
					Params:  pwaShareTargetParams{Title: "title", Text: "text", URL: "url"},
				}
			}
			return nil
		})
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "manifest_unavailable", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, manifest)
	}
}

// shareHandlerRequest é o corpo `application/x-www-form-urlencoded` (ou
// já decodificado pelo BFF como JSON equivalente — ver nota de escopo em
// goClient sobre quem faz o parsing) enviado pelo Web Share Target.
type shareHandlerRequest struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	URL   string `json:"url"`
}

// shareHandlerHandler implementa POST .../share-handler — dispara
// receiveMobileShareDataEvent via o MESMO Dispatcher.EmitEvent que
// cmd/server/events.go já usa para a rota genérica de evento nomeado,
// nunca um segundo mecanismo de despacho.
func shareHandlerHandler(tracker *shutdown.Tracker, db *database.DB, dispatcher *triggers.Dispatcher) http.HandlerFunc {
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
		idempotencyKey := r.Header.Get("Idempotency-Key")
		if idempotencyKey == "" {
			writeAPIError(w, http.StatusBadRequest, "idempotency_key_required", "cabeçalho Idempotency-Key é obrigatório")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_body", "não foi possível ler o corpo da requisição")
			return
		}
		var req shareHandlerRequest
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_json", "corpo da requisição não é um JSON válido")
				return
			}
		}
		payload := map[string]any{"title": req.Title, "text": req.Text, "url": req.URL}

		var fired int
		err = db.WithTenant(r.Context(), tenant, func(ctx context.Context, tx pgx.Tx) error {
			role, ok := resolveActorRole(ctx, tx, r, w)
			if !ok {
				return errHandled
			}
			if role == identity.RolePublic {
				writeAPIError(w, http.StatusUnauthorized, "login_required", "é necessário estar autenticado para compartilhar")
				return errHandled
			}
			shareTriggers, err := triggers.TriggersForEvent(ctx, tx, receiveMobileShareDataEvent)
			if err != nil {
				return err
			}
			if len(shareTriggers) == 0 {
				writeAPIError(w, http.StatusNotFound, "sharing_not_enabled", "compartilhamento não habilitado neste tenant")
				return errHandled
			}
			result, _, doErr := outbox.Do(ctx, tx, idempotencyKey, payload,
				func(ctx context.Context, tx pgx.Tx) (any, []outbox.Event, error) {
					n, err := dispatcher.EmitEvent(ctx, tx, tenant, role, receiveMobileShareDataEvent, actorUserContext(ctx, role), payload)
					if err != nil {
						return nil, nil, err
					}
					return float64(n), nil, nil
				})
			if doErr != nil {
				return doErr
			}
			fired = int(result.(float64))
			return nil
		})
		if err != nil {
			if errors.Is(err, errHandled) {
				return
			}
			if errors.Is(err, outbox.ErrKeyConflict) {
				writeAPIError(w, http.StatusConflict, "idempotency_key_conflict", "Idempotency-Key já foi usada com um payload diferente")
				return
			}
			writeAPIError(w, http.StatusBadGateway, "share_dispatch_failed", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, emitEventResponse{Fired: fired})
	}
}
