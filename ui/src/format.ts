const UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];

/** One decimal below 10 (except for plain bytes), none above. */
const fractionDigits = (exp: number, value: number) => (exp > 0 && value < 10 ? 1 : 0);

/**
 * Binary units without digit grouping (German "1.007 GiB" reads like one GiB). A value that would round to 1024
 * is shown in the next unit: 1048575 B is "1 MiB", not "1024 KiB".
 */
export function formatBytes(bytes: number, locale: string): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const last = UNITS.length - 1;
  let exp = 0;
  let value = bytes;
  while (value >= 1024 && exp < last) {
    value /= 1024;
    exp++;
  }
  let digits = fractionDigits(exp, value);
  if (exp < last && Number(value.toFixed(digits)) >= 1024) {
    value /= 1024;
    exp++;
    digits = fractionDigits(exp, value);
  }
  const text = new Intl.NumberFormat(locale, { maximumFractionDigits: digits, useGrouping: false }).format(
    value,
  );
  return `${text} ${UNITS[exp]}`;
}

export function formatRate(bytesPerSecond: number, locale: string): string {
  return `${formatBytes(bytesPerSecond, locale)}/s`;
}

export function percent(part: number, total: number): number {
  return total > 0 ? Math.min(100, Math.max(0, (part / total) * 100)) : 0;
}

/** A date of Docker (unix seconds or an RFC 3339 string); "–" for none (Docker's zero time is year 1). */
export function formatDate(value: number | string, locale: string): string {
  const d = typeof value === "number" ? new Date(value * 1000) : new Date(value);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 1971) return "–";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(d);
}
