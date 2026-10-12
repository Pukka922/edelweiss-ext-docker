import { describe, expect, it } from "vitest";
import { formatBytes, formatDate, formatRate, percent } from "./format";
import { pushSample } from "./history";

describe("format", () => {
  it("formats bytes with binary units and the locale's decimal separator", () => {
    expect(formatBytes(0, "en")).toBe("0 B");
    expect(formatBytes(512, "en")).toBe("512 B");
    expect(formatBytes(1536, "en")).toBe("1.5 KiB");
    expect(formatBytes(1536, "de")).toBe("1,5 KiB");
    expect(formatBytes(2 * 1024 ** 3, "en")).toBe("2 GiB");
    expect(formatRate(2048, "en")).toBe("2 KiB/s");
  });

  it("does not group digits: German 1.007 GiB would read like one GiB", () => {
    for (const locale of ["de", "en"]) {
      expect(formatBytes(1007 * 1024 ** 3, locale)).toBe("1007 GiB");
      expect(formatRate(1000 * 1024, locale)).toBe("1000 KiB/s");
    }
  });

  it("formats fractions of a byte as bytes", () => {
    for (const locale of ["de", "en"]) {
      expect(formatBytes(0.25, locale)).toBe("0 B");
      expect(formatBytes(0.9, locale)).toBe("1 B");
      expect(formatRate(0.25, locale)).toBe("0 B/s");
    }
  });

  it("moves to the next unit when the rounded value would display as 1024", () => {
    for (const locale of ["de", "en"]) {
      expect(formatBytes(1023, locale)).toBe("1023 B");
      expect(formatBytes(1023.6, locale)).toBe("1 KiB");
      expect(formatBytes(1048575, locale)).toBe("1 MiB"); // not "1024 KiB"
      expect(formatBytes(1048064, locale)).toBe("1 MiB"); // 1023.5 KiB rounds to 1024
      expect(formatBytes(1048063, locale)).toBe("1023 KiB");
      expect(formatBytes(1024 ** 3 - 1, locale)).toBe("1 GiB");
      expect(formatBytes(1024 ** 4 - 1, locale)).toBe("1 TiB");
      expect(formatBytes(1024, locale)).toBe("1 KiB");
      expect(formatBytes(1024 ** 3, locale)).toBe("1 GiB");
    }
  });

  it("stops at the largest unit", () => {
    expect(formatBytes(2048 * 1024 ** 5, "en")).toBe("2048 PiB");
    expect(formatBytes(1024 ** 6 - 1, "de")).toBe("1024 PiB");
  });

  it("computes bounded percentages", () => {
    expect(percent(25, 100)).toBe(25);
    expect(percent(5, 0)).toBe(0);
    expect(percent(150, 100)).toBe(100);
  });

  it("keeps the last samples", () => {
    expect(pushSample([1, 2, 3], 4, 3)).toEqual([2, 3, 4]);
    expect(pushSample([], 1)).toEqual([1]);
  });
});

describe("formatDate", () => {
  it("formats unix seconds and RFC 3339, and shows Docker's zero time as a dash", () => {
    expect(formatDate(0, "en")).toBe("–");
    expect(formatDate("0001-01-01T00:00:00Z", "en")).toBe("–");
    expect(formatDate("not a date", "en")).toBe("–");
    expect(formatDate(1791633600, "en")).toMatch(/2026/);
    expect(formatDate("2026-10-10T12:00:00Z", "de")).toMatch(/2026/);
  });
});
