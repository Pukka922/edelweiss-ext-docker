import { vi } from "vitest";

// xterm measures with a canvas when it loads, and jsdom only reports "Not implemented" for that. A page that has the
// console tab gets this stub; ConsoleTab.test.tsx brings a fake terminal of its own, which records what the tab does.
vi.mock("@xterm/xterm", () => ({ Terminal: class {} }));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class {} }));

if (!window.matchMedia) {
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}
// jsdom's scrollTo only logs "Not implemented"; the router scrolls to the top after every navigation
window.scrollTo = (() => {}) as typeof window.scrollTo;
if (!("ResizeObserver" in window)) {
  (window as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
}
