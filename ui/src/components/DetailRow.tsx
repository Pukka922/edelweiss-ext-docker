import type { JSX } from "solid-js";

/** A row of a description list: the label left (above on narrow screens), the value right. */
export function DetailRow(props: { label: string; children: JSX.Element }) {
  return (
    <div class="grid grid-cols-1 gap-1 py-1 sm:grid-cols-[12rem_1fr]">
      <dt class="text-sm text-base-content/70">{props.label}</dt>
      <dd class="text-sm">{props.children}</dd>
    </div>
  );
}
