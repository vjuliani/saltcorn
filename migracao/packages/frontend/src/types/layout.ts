// Documento de layout versionado (ADR-0002: "definir um documento de
// layout versionado, mesmo formato hoje produzido pelo builder e
// consumido por layout.ts"). O FORMATO da árvore em si (above/besides/
// contents/type) não muda aqui — é o mesmo que
// packages/saltcorn-builder/src/components/storage.js serializa e que
// packages/saltcorn-markup/layout.ts consome; o tipo `Layout` formal
// vive em @saltcorn/types/base_types (legado) e não é duplicado aqui.
//
// O que faltava e este arquivo fixa: um envelope explícito com número de
// versão, para o cliente tipado do BFF (bffClient.ts) e o builder saberem
// se o documento que estão lendo/gravando é compatível — sem isso,
// "versionar layouts" seria uma frase no ADR sem nenhum artefato
// correspondente no lado do frontend novo.
export const CURRENT_LAYOUT_VERSION = 1;

/**
 * LayoutSegment é deliberadamente `unknown`-shaped além de `type` — a
 * árvore completa (above/besides/contents/dezenas de campos por tipo de
 * segmento) já está definida e mantida em @saltcorn/types/base_types
 * (legado); duplicar essa definição aqui, parcialmente, seria pior do que
 * não tipá-la (uma cópia que diverge é mais perigosa que nenhuma cópia).
 * Este arquivo tipa só o ENVELOPE de versão, não o conteúdo.
 */
export interface LayoutSegment {
  type?: string;
  above?: LayoutSegment[];
  besides?: LayoutSegment[];
  contents?: LayoutSegment | LayoutSegment[] | string;
  [key: string]: unknown;
}

export interface VersionedLayout {
  version: typeof CURRENT_LAYOUT_VERSION;
  layout: LayoutSegment;
}

export function wrapLayout(layout: LayoutSegment): VersionedLayout {
  return { version: CURRENT_LAYOUT_VERSION, layout };
}

/**
 * unwrapLayout aceita tanto um documento já versionado quanto um
 * documento "cru" (o formato que o builder legado troca hoje, sem
 * envelope) — migração incremental: nenhum layout salvo antes desta
 * tarefa precisa ser reescrito para ganhar uma versão.
 */
export function unwrapLayout(doc: VersionedLayout | LayoutSegment): LayoutSegment {
  if (doc && typeof doc === "object" && "version" in doc && "layout" in doc) {
    return (doc as VersionedLayout).layout;
  }
  return doc as LayoutSegment;
}
