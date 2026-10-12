const round = (n: number) => Math.round(n * 10) / 10;

/** SVG polyline points for values in [0, max] inside a width × height box. */
export function sparklinePoints(values: readonly number[], width: number, height: number, max = 100): string {
  if (values.length === 0) return "";
  const step = values.length > 1 ? width / (values.length - 1) : 0;
  return values
    .map((v, i) => `${round(i * step)},${round(height - (Math.min(Math.max(v, 0), max) / max) * height)}`)
    .join(" ");
}

export function Sparkline(props: { values: number[]; max?: number }) {
  return (
    <svg class="h-10 w-full text-primary" viewBox="0 0 120 40" preserveAspectRatio="none" aria-hidden="true">
      <polyline
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        vector-effect="non-scaling-stroke"
        points={sparklinePoints(props.values, 120, 40, props.max ?? 100)}
      />
    </svg>
  );
}
