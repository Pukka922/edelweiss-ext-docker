/** Appends value and keeps the last max samples. */
export function pushSample(values: readonly number[], value: number, max = 60): number[] {
  const next = [...values, value];
  return next.length > max ? next.slice(next.length - max) : next;
}
