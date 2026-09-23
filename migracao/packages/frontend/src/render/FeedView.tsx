// Desenha o DTO de renderização de uma view "Feed" (GO-051,
// internal/views/feed.go do lado Go) — para cada linha da tabela, a view
// Show configurada (`show_view`), dentro de um card. O mecanismo real é
// literalmente "mostrar N cards, cada um a mesma <ShowView> que a view
// Show standalone já desenha" — nenhuma lógica de apresentação nova
// além de empacotar em cards + paginação (mesmo padrão de cursor de
// ListView).
import { useT } from "../i18n/I18nContext";
import { ShowView, type ShowViewPlan } from "./ShowView";

export interface FeedViewCard {
  record_id: number;
  show: ShowViewPlan;
}

export interface FeedViewPlan {
  view_id: number;
  table: string;
  cards: FeedViewCard[];
  view_to_create_id?: number;
  view_to_create_name?: string;
  next_cursor?: string | null;
}

export interface FeedViewProps {
  plan: FeedViewPlan;
  onNextPage?: (cursor: string) => void;
  // onCreateClick (GO-051) — o pai decide o que "criar" significa (ex.:
  // ViewsListPage troca a pré-visualização para view_to_create_id,
  // reaproveitando o mesmo mecanismo de handlePreview que qualquer
  // outro botão "Visualizar" já usa — nenhum roteador novo).
  onCreateClick?: () => void;
}

export function FeedView({ plan, onNextPage, onCreateClick }: FeedViewProps) {
  const t = useT();
  return (
    <div data-testid="feed-view">
      {plan.view_to_create_id ? (
        <button type="button" className="btn btn-primary mb-3" data-testid="feed-create-button" onClick={onCreateClick}>
          {t("feed.create")}
        </button>
      ) : null}
      {plan.cards.length === 0 ? (
        <p data-testid="feed-empty">{t("feed.empty")}</p>
      ) : (
        <div className="row">
          {plan.cards.map((card) => (
            <div className="col-12 col-md-6 col-lg-4 mb-3" key={card.record_id} data-testid={`feed-card-${card.record_id}`}>
              <div className="card">
                <div className="card-body">
                  <ShowView plan={card.show} />
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
      {plan.next_cursor ? (
        <button type="button" className="btn btn-outline-secondary" onClick={() => onNextPage?.(plan.next_cursor as string)}>
          {t("list.nextPage")}
        </button>
      ) : null}
    </div>
  );
}
