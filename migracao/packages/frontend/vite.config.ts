import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Proxy de /api/bff para o BFF real, em dev (`npm run dev`) e preview
// (`vite preview`, usado pelo harness de E2E de GO-021,
// migracao/e2e/run.sh) — evita CORS entre este frontend (porta própria) e
// o BFF (porta separada) quando os dois não estão atrás do MESMO proxy
// reverso, como estão em produção (ver App.tsx: `VITE_BFF_BASE_URL` vazio
// = mesma origem). Achado do E2E de navegador real (GO-021): sem este
// proxy, todo `fetch()` de dentro do navegador para o BFF falha com
// "blocked by CORS policy" — algo que nenhum teste anterior (jsdom/
// vitest, HTTP direto sem navegador) exercitava. Alvo configurável via
// SALTCORN_DEV_BFF_PROXY_TARGET; padrão aponta para o BFF local na porta
// documentada no README do BFF.
const bffProxyTarget = process.env.SALTCORN_DEV_BFF_PROXY_TARGET ?? "http://localhost:3100";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api/bff": { target: bffProxyTarget, changeOrigin: true },
    },
  },
  preview: {
    proxy: {
      "/api/bff": { target: bffProxyTarget, changeOrigin: true },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./test/setup.ts"],
  },
});
