import "@testing-library/jest-dom/vitest";

// next-themes calls matchMedia; jsdom doesn't provide it
if (typeof window.matchMedia === "undefined") {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}

// CodeMirror measures text geometry through Range.getClientRects, which jsdom
// leaves unimplemented. Without a stub its measure pass throws partway through,
// which makes any editor-bearing test flaky and times out neighbours under
// parallel load. Zero-size rects are fine: the tests check DOM content, not
// pixel positions.
{
  const zeroRect = (): DOMRect => ({
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    width: 0,
    height: 0,
    toJSON: () => ({}),
  });
  const emptyRectList = (): DOMRectList => {
    const list = { length: 0, item: () => null } as unknown as DOMRectList;
    (list as unknown as { [Symbol.iterator]: () => Iterator<DOMRect> })[
      Symbol.iterator
    ] = function* () {};
    return list;
  };
  if (typeof Range !== "undefined") {
    Range.prototype.getClientRects = emptyRectList;
    Range.prototype.getBoundingClientRect = zeroRect;
  }
}

// Mock sessionStorage
const store: Record<string, string> = {};
const mockSessionStorage = {
  getItem: (key: string) => store[key] ?? null,
  setItem: (key: string, value: string) => {
    store[key] = value;
  },
  removeItem: (key: string) => {
    delete store[key];
  },
  clear: () => {
    for (const key of Object.keys(store)) delete store[key];
  },
  get length() {
    return Object.keys(store).length;
  },
  key: (index: number) => Object.keys(store)[index] ?? null,
};

Object.defineProperty(window, "sessionStorage", {
  value: mockSessionStorage,
});
