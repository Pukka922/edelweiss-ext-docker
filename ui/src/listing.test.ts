import { describe, expect, it } from "vitest";
import { compareNames, matches, PAGE_SIZE, paginate } from "./listing";

describe("compareNames", () => {
  it("ignores case", () => {
    expect(compareNames("Web", "web")).not.toBe(compareNames("web", "Web")); // only the tie-break tells them apart
    expect(compareNames("apple", "Banana")).toBeLessThan(0);
    expect(compareNames("Banana", "apple")).toBeGreaterThan(0);
    expect(["b", "A", "c", "B", "a"].sort(compareNames).map((s) => s.toLowerCase())).toEqual([
      "a",
      "a",
      "b",
      "b",
      "c",
    ]);
  });

  it("sorts numbers naturally", () => {
    expect(["web10", "web2", "web1"].sort(compareNames)).toEqual(["web1", "web2", "web10"]);
  });

  it("is a stable total order: names the collator calls equal are told apart by code units", () => {
    expect(compareNames("a", "a")).toBe(0);
    expect(compareNames("A", "a")).toBe(-compareNames("a", "A"));
    expect(compareNames("A", "a")).not.toBe(0);
    expect(["a", "A"].sort(compareNames)).toEqual(["A", "a"].sort(compareNames));
    expect(["A", "a"].sort(compareNames)).toEqual(["A", "a"]);
  });
});

describe("matches", () => {
  it("matches everything for an empty or blank query", () => {
    expect(matches("", "web")).toBe(true);
    expect(matches("   ", "web")).toBe(true);
    expect(matches("", undefined)).toBe(true);
  });

  it("needs every word in at least one field", () => {
    expect(matches("nginx 8080", "web", "nginx:1.29", "8080")).toBe(true);
    expect(matches("nginx 9090", "web", "nginx:1.29", "8080")).toBe(false);
    expect(matches("  ngi   web ", "web", "nginx:1.29")).toBe(true);
    expect(matches("web", undefined, "db")).toBe(false);
  });

  it("ignores case", () => {
    expect(matches("NGINX", "web", "nginx:1.29")).toBe(true);
    expect(matches("web", "WEB")).toBe(true);
  });
});

describe("paginate", () => {
  it("has one page for an empty list", () => {
    expect(paginate([], 1)).toEqual({ rows: [], page: 1, pages: 1 });
  });

  it("clamps the page", () => {
    const items = Array.from({ length: 30 }, (_, i) => i);
    expect(paginate(items, 0).page).toBe(1);
    expect(paginate(items, -4).page).toBe(1);
    expect(paginate(items, 9).page).toBe(2);
  });

  it("slices the page", () => {
    const items = Array.from({ length: 30 }, (_, i) => i);
    expect(PAGE_SIZE).toBe(25);
    const first = paginate(items, 1);
    expect(first.pages).toBe(2);
    expect(first.rows).toEqual(items.slice(0, 25));
    expect(paginate(items, 2).rows).toEqual(items.slice(25));
    expect(paginate(items, 2, 10)).toEqual({ rows: items.slice(10, 20), page: 2, pages: 3 });
    expect(paginate(items.slice(0, 25), 1).pages).toBe(1);
  });
});
