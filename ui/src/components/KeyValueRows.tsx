import { useI18n } from "@edelweiss/sdk";
import { Index } from "solid-js";

export interface KV {
  key: string;
  value: string;
}

export function toMap(rows: KV[]): Record<string, string> {
  return Object.fromEntries(rows.filter((r) => r.key.trim() !== "").map((r) => [r.key.trim(), r.value]));
}

export function fromMap(map: Record<string, string> | undefined): KV[] {
  return Object.entries(map ?? {}).map(([key, value]) => ({ key, value }));
}

/** Editable key/value rows (driver options). */
export function KeyValueRows(props: { rows: KV[]; onChange: (rows: KV[]) => void; label: string }) {
  const { t } = useI18n();
  const set = (i: number, patch: Partial<KV>) =>
    props.onChange(props.rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <fieldset class="flex flex-col gap-2">
      <legend class="text-sm font-medium">{props.label}</legend>
      <Index each={props.rows}>
        {(row, i) => (
          <div class="flex gap-2">
            <input
              class="input input-sm w-40"
              aria-label={t("kv.key")}
              value={row().key}
              onInput={(e) => set(i, { key: e.currentTarget.value })}
            />
            <input
              class="input input-sm flex-1"
              aria-label={t("kv.value")}
              value={row().value}
              onInput={(e) => set(i, { value: e.currentTarget.value })}
            />
            <button
              type="button"
              class="btn btn-sm btn-ghost"
              onClick={() => props.onChange(props.rows.filter((_, j) => j !== i))}
            >
              {t("actions.remove")}
            </button>
          </div>
        )}
      </Index>
      <button
        type="button"
        class="btn btn-sm btn-ghost self-start"
        onClick={() => props.onChange([...props.rows, { key: "", value: "" }])}
      >
        {t("kv.add")}
      </button>
    </fieldset>
  );
}
