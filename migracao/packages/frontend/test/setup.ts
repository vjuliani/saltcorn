import "@testing-library/jest-dom/vitest";

// ResizeObserver (GO-048) — jsdom não implementa a API que @xyflow/react
// usa internamente para medir o container do canvas; sem este polyfill,
// qualquer teste que monte <ReactFlow> lança "ResizeObserver is not
// defined" antes mesmo de renderizar. Um no-op é suficiente: os testes
// não dependem de medição real de layout, só de que os nós/arestas e o
// comportamento (clique, drag, chamadas ao BffClient) funcionem.
if (typeof globalThis.ResizeObserver === "undefined") {
  class ResizeObserverPolyfill {
    observe() {}
    unobserve() {}
    disconnect() {}
  }
  globalThis.ResizeObserver = ResizeObserverPolyfill as unknown as typeof ResizeObserver;
}
