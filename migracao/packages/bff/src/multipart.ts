// Parser multipart mínimo (GO-051) — mesma disciplina de "dependências
// mínimas, deliberadamente" já aplicada a session.ts/csrf.ts (superfície
// pequena e bem entendida: UM campo de arquivo esperado, "file", nunca um
// parser multipart genérico de propósito geral). Usado só por
// POST /api/bff/files.
export interface ParsedFile {
  filename: string;
  contentType: string;
  content: Buffer;
}

const CRLF = "\r\n";

/**
 * parseSingleFileMultipart extrai o campo `fieldName` (esperado ser um
 * arquivo, com `filename=`) de um corpo multipart/form-data — devolve
 * null se o Content-Type não é multipart, o boundary está ausente, ou o
 * campo não foi encontrado. Opera em Buffer puro (nunca decodifica o
 * CONTEÚDO do arquivo como string — só os cabeçalhos de cada parte, que
 * são sempre ASCII/UTF-8 por definição do formato).
 */
export function parseSingleFileMultipart(contentTypeHeader: string | undefined, body: Buffer, fieldName: string): ParsedFile | null {
  if (!contentTypeHeader) return null;
  const boundaryMatch = /boundary=(?:"([^"]+)"|([^;]+))/i.exec(contentTypeHeader);
  const boundary = (boundaryMatch?.[1] ?? boundaryMatch?.[2])?.trim();
  if (!boundary) return null;
  const delimiter = Buffer.from(`--${boundary}`);
  const crlfcrlf = Buffer.from(CRLF + CRLF);

  let searchStart = 0;
  for (;;) {
    const start = body.indexOf(delimiter, searchStart);
    if (start === -1) return null;
    let partStart = start + delimiter.length;
    if (body.subarray(partStart, partStart + 2).toString("latin1") === "--") return null; // "--boundary--" final
    if (body.subarray(partStart, partStart + 2).toString("latin1") === CRLF) partStart += 2;

    const headerEnd = body.indexOf(crlfcrlf, partStart);
    if (headerEnd === -1) return null;
    const headerText = body.subarray(partStart, headerEnd).toString("utf8");
    const contentStart = headerEnd + crlfcrlf.length;
    const nextDelimiter = body.indexOf(delimiter, contentStart);
    if (nextDelimiter === -1) return null;
    const contentEnd = Math.max(contentStart, nextDelimiter - 2); // -2: o \r\n antes do próximo delimitador

    const nameMatch = /name="([^"]*)"/i.exec(headerText);
    const filenameMatch = /filename="([^"]*)"/i.exec(headerText);
    if (nameMatch?.[1] === fieldName && filenameMatch) {
      const contentTypeMatch = /content-type:\s*([^\r\n]+)/i.exec(headerText);
      return {
        filename: filenameMatch[1] || "upload",
        contentType: contentTypeMatch?.[1]?.trim() ?? "application/octet-stream",
        content: body.subarray(contentStart, contentEnd),
      };
    }
    searchStart = nextDelimiter;
  }
}
