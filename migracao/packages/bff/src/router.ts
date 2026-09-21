// Router mínimo — o BFF tem 5 rotas (/healthz, /readyz, bootstrap, e
// GET/POST de records); um roteador de terceiros seria mais superfície de
// dependência do que valor para 5 padrões de rota. Suporta segmentos
// `:nome` (ex.: "/api/bff/tables/:table/records"), método HTTP exato, e
// devolve os parâmetros extraídos.
import type { IncomingMessage, ServerResponse } from "node:http";

export interface RouteParams {
  readonly [key: string]: string;
}

export type Handler = (req: IncomingMessage, res: ServerResponse, params: RouteParams) => Promise<void> | void;

interface Route {
  readonly method: string;
  readonly segments: readonly string[];
  readonly handler: Handler;
}

export class Router {
  private readonly routes: Route[] = [];

  add(method: string, pattern: string, handler: Handler): void {
    this.routes.push({ method, segments: splitPath(pattern), handler });
  }

  get(pattern: string, handler: Handler): void {
    this.add("GET", pattern, handler);
  }

  post(pattern: string, handler: Handler): void {
    this.add("POST", pattern, handler);
  }

  patch(pattern: string, handler: Handler): void {
    this.add("PATCH", pattern, handler);
  }

  // delete (GO-044) — a administração de usuário precisa de DELETE
  // /api/bff/admin/users/:id; nenhuma rota anterior precisava do verbo.
  delete(pattern: string, handler: Handler): void {
    this.add("DELETE", pattern, handler);
  }

  /** match encontra a primeira rota cujo método e forma de path batem — retorna null se nenhuma bater (o chamador decide 404 vs. 405). */
  match(method: string, pathname: string): { handler: Handler; params: RouteParams } | null {
    const pathSegments = splitPath(pathname);
    for (const route of this.routes) {
      if (route.method !== method) continue;
      const params = matchSegments(route.segments, pathSegments);
      if (params) return { handler: route.handler, params };
    }
    return null;
  }
}

function splitPath(pathname: string): string[] {
  return pathname.split("/").filter((s) => s.length > 0);
}

function matchSegments(pattern: readonly string[], actual: readonly string[]): RouteParams | null {
  if (pattern.length !== actual.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < pattern.length; i++) {
    const p = pattern[i]!;
    const a = actual[i]!;
    if (p.startsWith(":")) {
      params[p.slice(1)] = decodeURIComponent(a);
    } else if (p !== a) {
      return null;
    }
  }
  return params;
}
