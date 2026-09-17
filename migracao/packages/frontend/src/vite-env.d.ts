/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** URL base do BFF (GO-019) — vazio/relativo assume mesmo-origin via proxy reverso em produção. */
  readonly VITE_BFF_BASE_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
