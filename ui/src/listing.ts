import { createMemo, createRoot, createSignal } from "solid-js";

// One collator for all names: case does not matter, and numbers sort naturally (web2 before web10).
const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/** Orders names for the lists. What the collator calls equal (web, Web) is told apart by code units: a total, stable order. */
export function compareNames(a: string, b: string): number {
  return collator.compare(a, b) || (a < b ? -1 : a > b ? 1 : 0);
}

/** Whether every word of the query is a case-insensitive part of at least one field. An empty query matches all. */
export function matches(query: string, ...fields: (string | undefined)[]): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const haystack = fields.map((f) => (f ?? "").toLowerCase());
  return words.every((w) => haystack.some((f) => f.includes(w)));
}

/** The entries a list shows per page. */
export const PAGE_SIZE = 25;

/** The rows of one page; the page is clamped to 1..pages, and an empty list has one page. */
export function paginate<T>(
  items: T[],
  page: number,
  size = PAGE_SIZE,
): { rows: T[]; page: number; pages: number } {
  const pages = Math.max(1, Math.ceil(items.length / size));
  const shown = Math.min(Math.max(1, Math.trunc(page) || 1), pages);
  return { rows: items.slice((shown - 1) * size, shown * size), page: shown, pages };
}

interface ListState {
  search: () => string;
  setSearch: (value: string) => void;
  page: () => number;
  setPage: (page: number) => void;
}

// In a module, not in the page: a list keeps its search and page while a detail page is open, and until a reload.
const states = new Map<string, ListState>();

/** The search and the page of the list under that key. A new search goes back to page 1. */
export function listState(key: string): ListState {
  let state = states.get(key);
  if (!state) {
    state = createRoot(() => {
      const [search, setSearch] = createSignal("");
      const [page, setPage] = createSignal(1);
      return {
        search,
        setSearch: (value: string) => {
          setSearch(value);
          setPage(1);
        },
        page,
        setPage,
      };
    });
    states.set(key, state);
  }
  return state;
}

/** Forgets every search and page (for the tests: the state would carry over from one to the next). */
export function resetListStates() {
  states.clear();
}

/**
 * A list's rows: the items (already sorted) that the search finds in the fields of each, and one page of them. The
 * page that is shown is clamped, so a list that shrinks on a reload shows its last page; the page asked for stays.
 */
export function useList<T>(
  key: string,
  items: () => T[] | undefined,
  fields: (item: T) => (string | undefined)[],
  keep: (item: T) => boolean = () => true,
) {
  const state = listState(key);
  const found = createMemo(() => {
    const query = state.search();
    return (items() ?? []).filter((item) => keep(item) && matches(query, ...fields(item)));
  });
  const paged = createMemo(() => paginate(found(), state.page()));
  return {
    search: state.search,
    setSearch: state.setSearch,
    setPage: state.setPage,
    /** How many items the search finds. */
    count: () => found().length,
    rows: () => paged().rows,
    page: () => paged().page,
    pages: () => paged().pages,
  };
}
