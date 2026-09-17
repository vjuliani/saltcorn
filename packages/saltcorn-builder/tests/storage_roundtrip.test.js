// Round-trip real de layoutToNodes ⇄ craftToSaltcorn (GO-018, critério de
// aceite "round-trip preserva propriedades, referências e extensões") —
// lacuna encontrada durante a investigação: nenhum teste existente
// exercitava esse par de funções, só componentes de configuração
// individuais (relations_finder.test.js). Usa um Editor real do Craft.js
// (não mockado) montado no ambiente jsdom global já fornecido por
// setup-node-runner.js, para provar que o par de funções realmente
// preserva dado através de um ciclo completo desserializar→re-serializar,
// não apenas que as funções existem.
import {
  describe,
  it,
  expect,
} from "@saltcorn/db-common/test_expect";
import React from "react";
import { createRoot } from "react-dom/client";
import { act } from "react-dom/test-utils";
import { Editor, Frame, Element, useEditor } from "@craftjs/core";

import { Text } from "../src/components/elements/Text";
import { LibraryInstance } from "../src/components/elements/LibraryInstance";
import { Column } from "../src/components/elements/Column";

const { layoutToNodes, craftToSaltcorn } = require("../src/components/storage.js");

global.IS_REACT_ACT_ENVIRONMENT = true;

// Harness mínimo: monta um Editor real do Craft.js — <Frame><Element canvas
// is={Column}></Element></Frame> é exatamente como Builder.js estabelece o
// nó ROOT de verdade (ver Builder.js linha ~1084) - sem isso, addNodeTree
// falha com "Node does not exist" porque <Editor> sozinho não cria ROOT,
// só <Frame> cria. Expõe query/actions ao chamador via um ref mutável
// (useEditor só existe dentro da árvore do Editor), e roda layoutToNodes
// uma vez, na montagem, exatamente como NextButton faz em produção
// (Builder.js linha ~798).
function mountEditorWithLayout(layout) {
  const container = document.createElement("div");
  document.body.appendChild(container);
  const root = createRoot(container);
  const editorRef = {};

  function Capture() {
    const { query, actions } = useEditor();
    editorRef.query = query;
    editorRef.actions = actions;
    React.useEffect(() => {
      layoutToNodes(layout, query, actions, "ROOT", {});
    }, []);
    return null;
  }

  act(() => {
    root.render(
      <Editor resolver={{ Text, LibraryInstance, Column }}>
        <Capture />
        <Frame>
          <Element canvas is={Column}></Element>
        </Frame>
      </Editor>
    );
  });

  return {
    editorRef,
    cleanup: () => {
      act(() => {
        root.unmount();
      });
      container.remove();
    },
  };
}

describe("storage round-trip (layoutToNodes <-> craftToSaltcorn)", () => {
  it("preserva propriedades (texto) e extensões (_custom) de um segmento simples", () => {
    const layout = {
      above: [
        {
          type: "blank",
          contents: "Hello world",
          _custom: { foo: "bar" },
        },
      ],
    };

    const { editorRef, cleanup } = mountEditorWithLayout(layout);
    try {
      const serialized = JSON.parse(editorRef.query.serialize());
      const { layout: seg } = craftToSaltcorn(serialized, "ROOT", {});

      // Um único item em "above" não fica envelopado (ver get_nodes em
      // storage.js: nodes.length === 1 retorna o segmento diretamente) —
      // o resultado É o segmento, não {above: [segmento]}.
      expect(seg.type).toEqual("blank");
      expect(seg.contents).toEqual("Hello world"); // propriedade
      expect(seg._custom).toEqual({ foo: "bar" }); // extensão
    } finally {
      cleanup();
    }
  });

  it("preserva a referência (library_id) de um componente compartilhado", () => {
    // contents/library_name já preenchidos aqui deliberadamente (o que
    // resolveLibraryRefs faria antes de layoutToNodes rodar, ver comentário
    // em storage.js linha ~318) - não precisamos mockar fetch para provar
    // que a REFERÊNCIA (library_id) sobrevive ao ciclo completo de
    // nós reais do Craft.js.
    const layout = {
      above: [
        {
          type: "library",
          library_id: 7,
          library_name: "MyLib",
          contents: { type: "blank", contents: "lib text" },
        },
      ],
    };

    const { editorRef, cleanup } = mountEditorWithLayout(layout);
    try {
      const serialized = JSON.parse(editorRef.query.serialize());
      const { layout: seg } = craftToSaltcorn(serialized, "ROOT", {});

      expect(seg.type).toEqual("library");
      expect(seg.library_id).toEqual(7); // referência
    } finally {
      cleanup();
    }
  });

  it("preserva propriedades E extensões quando o segmento está dentro de uma referência de biblioteca", () => {
    const layout = {
      above: [
        {
          type: "library",
          library_id: 9,
          library_name: "Nested",
          contents: {
            type: "blank",
            contents: "conteúdo da biblioteca",
            _custom: { tag: "shared" },
          },
        },
      ],
    };

    const { editorRef, cleanup } = mountEditorWithLayout(layout);
    try {
      const serialized = JSON.parse(editorRef.query.serialize());
      const craftResult = craftToSaltcorn(serialized, "ROOT", {});
      const seg = craftResult.layout;

      expect(seg.type).toEqual("library");
      expect(seg.library_id).toEqual(9);
      // craftToSaltcorn separa o conteúdo de uma LibraryInstance em
      // libraryUpdates (ele pertence à linha _sc_library compartilhada, não
      // a esta página) - não fica em seg.contents. Verificamos aqui que o
      // conteúdo (propriedade + extensão) não se perdeu, só mudou de lugar.
      expect(craftResult.libraryUpdates).toHaveLength(1);
      const libUpdate = craftResult.libraryUpdates[0];
      expect(libUpdate.library_id).toEqual(9);
      expect(libUpdate.layout.contents).toEqual("conteúdo da biblioteca");
      expect(libUpdate.layout._custom).toEqual({ tag: "shared" });
    } finally {
      cleanup();
    }
  });
});
