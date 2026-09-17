// Testes do BffClient (GO-019): createTable/addField/createView/getView/
// updateView, com fetch mockado (não é integração real com o BFF — isso
// já tem cobertura em migracao/packages/bff/test/editor.test.ts e em
// cmd/server/views_test.go do lado Go). O que importa provar aqui é que
// o BffClient monta a requisição certa e distingue ViewConflictError de
// qualquer outro erro.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { BffClient, BffClientError, ViewConflictError, ViewUnsupportedError, readCsrfCookie } from "../src/bffClient";

describe("readCsrfCookie", () => {
  it("extrai sc_csrf de uma string de cookies com múltiplos valores", () => {
    expect(readCsrfCookie("sc_session=abc; sc_csrf=tok123; outro=x")).toEqual("tok123");
  });

  it("retorna undefined quando sc_csrf não está presente", () => {
    expect(readCsrfCookie("sc_session=abc")).toBeUndefined();
  });

  it("decodifica valores URL-encoded", () => {
    expect(readCsrfCookie("sc_csrf=a%2Fb%3Dc")).toEqual("a/b=c");
  });
});

describe("BffClient — ciclo do editor (GO-019)", () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function jsonResponse(status: number, body: unknown) {
    return { ok: status >= 200 && status < 300, status, json: async () => body } as Response;
  }

  it("createTable envia POST /api/bff/tables com CSRF", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(201, { id: 1, name: "books" }));
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    const result = await client.createTable({ name: "books" });
    expect(result).toEqual({ id: 1, name: "books" });
    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toEqual("http://bff.local/api/bff/tables");
    expect((init.headers as Record<string, string>)["X-CSRF-Token"]).toEqual("tok");
  });

  it("createTable sem csrfToken configurado lança BffClientError, nunca chama fetch", async () => {
    const client = new BffClient({ baseUrl: "http://bff.local" });
    await expect(client.createTable({ name: "books" })).rejects.toThrow(BffClientError);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("addField envia POST /api/bff/tables/:table/fields", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(201, { id: 1, table_id: 1, name: "title", type: "text" }));
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    await client.addField("books", { name: "title", type: "text", required: true });
    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toEqual("http://bff.local/api/bff/tables/books/fields");
  });

  it("createView e getView compõem o ciclo de criar/reabrir", async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse(201, { id: 5, name: "booklist", _version: "1" }))
      .mockResolvedValueOnce(jsonResponse(200, { id: 5, name: "booklist", _version: "1" }));
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });

    const created = await client.createView({ name: "booklist", table: "books", template: "List", configuration: {} });
    expect(created.id).toEqual(5);

    const reopened = await client.getView(5);
    expect(reopened.id).toEqual(5);
    const [getUrl, getInit] = fetchMock.mock.calls[1]!;
    expect(getUrl).toEqual("http://bff.local/api/bff/views/5");
    expect(getInit.method).toEqual("GET");
  });

  it("updateView em 409 version_conflict lança ViewConflictError, distinguível de outros erros", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(409, { error: { code: "version_conflict", message: "a view foi modificada por outra transação" } })
    );
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    await expect(client.updateView(5, { _version: "1", configuration: {} })).rejects.toThrow(ViewConflictError);
  });

  it("updateView em outro erro (não version_conflict) lança BffClientError comum, não ViewConflictError", async () => {
    fetchMock.mockResolvedValue(jsonResponse(403, { error: { code: "not_authorized", message: "sem permissão" } }));
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    await expect(client.updateView(5, { _version: "1" })).rejects.toThrow(BffClientError);
    await expect(client.updateView(5, { _version: "1" })).rejects.not.toBeInstanceOf(ViewConflictError);
  });

  it("updateView publica via min_role, sem precisar de configuration", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, { id: 5, min_role: 100, _version: "2" }));
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    const published = await client.updateView(5, { _version: "1", min_role: 100 });
    expect(published.min_role).toEqual(100);
    const [, init] = fetchMock.mock.calls[0]!;
    expect(JSON.parse(init.body as string)).toEqual({ _version: "1", min_role: 100 });
  });

  it("updateView em 422 view_unsupported lança ViewUnsupportedError com o motivo do Go (GO-020)", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(422, { error: { code: "view_unsupported", message: 'template "Show" não suportado neste runtime (só "List")' } })
    );
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    const err = await client.updateView(5, { _version: "1", min_role: 100 }).catch((e) => e);
    expect(err).toBeInstanceOf(ViewUnsupportedError);
    expect((err as ViewUnsupportedError).message).toEqual('template "Show" não suportado neste runtime (só "List")');
  });

  it("listViews envia GET /api/bff/views, com filtro opcional por tabela (GO-020)", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse(200, [{ id: 5, name: "booklist" }]));
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    const list = await client.listViews("books");
    expect(list).toEqual([{ id: 5, name: "booklist" }]);
    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toEqual("http://bff.local/api/bff/views?table=books");
  });

  it("renderView devolve o plano de colunas/linhas/paginação (GO-020)", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(200, { view_id: 5, columns: [{ field_name: "title", header_label: "Título" }], rows: [{ title: "Dune" }], order_by: "id", descending: false, next_cursor: null })
    );
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    const plan = await client.renderView(5, { limit: 2 });
    expect(plan.rows).toEqual([{ title: "Dune" }]);
    const [url] = fetchMock.mock.calls[0]!;
    expect(url).toEqual("http://bff.local/api/bff/views/5/render?limit=2");
  });

  it("renderView em 422 view_unsupported lança ViewUnsupportedError, distinguível de outros erros (GO-020)", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(422, { error: { code: "view_unsupported", message: "layout.besides ausente" } })
    );
    const client = new BffClient({ baseUrl: "http://bff.local", csrfToken: "tok" });
    await expect(client.renderView(5)).rejects.toThrow(ViewUnsupportedError);
  });
});
