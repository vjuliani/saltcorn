// Integra o builder Craft.js EXISTENTE (packages/saltcorn-builder) como um
// widget imperativo isolado — exatamente o padrão que ADR-0002 pede
// ("encapsular widgets imperativos, para que React e scripts legados
// nunca manipulem o mesmo nó do DOM simultaneamente"), aplicado ao
// próprio builder em vez de reescrevê-lo: ele já é React (Craft.js), mas
// é uma árvore React DIFERENTE, empacotada como bundle UMD próprio
// (public/vendor/builder_bundle.js, cópia do dist/ já publicado por
// packages/saltcorn-builder — não reconstruído aqui, ver README) — com
// sua própria cópia de React. Montá-lo dentro da MESMA árvore React deste
// shell arriscaria exatamente o tipo de conflito de versão/instância que
// ADR-0002 quer evitar; em vez disso, este componente cria uma div
// contêiner que o React deste shell NUNCA mais toca depois de montada, e
// delega tudo dentro dela para `window.builder.renderBuilder` (a mesma
// função que páginas legadas já chamam hoje, GO-001 §2.3) — uma fronteira
// de widget real, não uma reimplementação.
import { useEffect, useId, useRef } from "react";
import type { LayoutSegment } from "../types/layout";

declare global {
  interface Window {
    builder?: {
      renderBuilder: (containerId: string, encodedOptions: string, encodedLayout: string, mode: string) => void;
    };
  }
}

const BUNDLE_SRC = "/vendor/builder_bundle.js";
let bundleLoadPromise: Promise<void> | null = null;

function loadBuilderBundle(): Promise<void> {
  if (window.builder) return Promise.resolve();
  if (bundleLoadPromise) return bundleLoadPromise;
  bundleLoadPromise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = BUNDLE_SRC;
    script.onload = () => resolve();
    script.onerror = () => reject(new Error(`falha ao carregar ${BUNDLE_SRC}`));
    document.head.appendChild(script);
  });
  return bundleLoadPromise;
}

export interface BuilderPanelProps {
  layout: LayoutSegment;
  options: Record<string, unknown>;
  mode?: string;
}

export function BuilderPanel({ layout, options, mode = "page" }: BuilderPanelProps) {
  const reactId = useId();
  const containerId = `builder-container-${reactId.replace(/[:]/g, "")}`;
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    loadBuilderBundle().then(() => {
      if (cancelled || !window.builder) return;
      window.builder.renderBuilder(
        containerId,
        encodeURIComponent(JSON.stringify(options)),
        encodeURIComponent(JSON.stringify(layout)),
        mode
      );
    });
    return () => {
      cancelled = true;
      // O bundle guarda a raiz React em container.__scRoot (ver
      // dist/builder_bundle.js) — sem desmontar, um segundo mount no
      // mesmo id reaproveita a raiz (comportamento herdado do legado,
      // que nunca desmonta o builder na mesma página); aqui, ao trocar
      // de painel, o container em si é removido do DOM pelo React deste
      // shell, o que é seguro porque nunca é o React deste shell quem
      // manipula o CONTEÚDO da div — só a existência dela.
    };
  }, [containerId, JSON.stringify(layout), JSON.stringify(options), mode]);

  return <div id={containerId} ref={containerRef} data-testid="builder-container" />;
}

/**
 * Layout/options mínimos para demonstrar o builder funcionando sem um BFF
 * real (critério de aceite "builder funciona com mocks") — um segmento de
 * texto simples, sem nenhuma chamada de rede (nenhuma referência de
 * biblioteca no layout, então resolveLibraryRefs não faz fetch nenhum).
 */
export const MOCK_BUILDER_LAYOUT: LayoutSegment = {
  above: [
    {
      type: "blank",
      contents: "Bem-vindo ao editor (dados de mock, GO-018)",
    },
  ],
};

export const MOCK_BUILDER_OPTIONS: Record<string, unknown> = {
  mode: "page",
  fields: [],
  fieldViews: {},
  icons: [],
  csrfToken: "mock-csrf-token",
  isRTL: false,
};
