import { useI18n } from "@edelweiss/sdk";
import { createComputed, createMemo, createSignal, createUniqueId, For, Show } from "solid-js";
import type { Volume } from "../api";
import { Input, Select } from "../components/Field";
import { errorText } from "../errors";

const NEW = "__new__";

/**
 * The source of a volume row: one of the named volumes, or a new volume with a name of its own. Without the list (it
 * could not be loaded) a name field as before. A volume of the row that is not in the list shows as "not found".
 */
export function VolumeSelect(props: {
  value: string;
  volumes: Volume[] | undefined;
  failed: unknown;
  invalid: boolean;
  onChange: (name: string) => void;
}) {
  const { t } = useI18n();
  const errorId = createUniqueId();
  const named = createMemo(() =>
    (props.volumes ?? []).filter((v) => !v.anonymous).sort((a, b) => a.name.localeCompare(b.name)),
  );
  const known = () => named().some((v) => v.name === props.value);
  // "New volume …" is a state that latches: the user chose it, or it is preselected because there is no volume to
  // choose (that is known only when the list has arrived). Typing a name does not end it, choosing another entry does.
  const [isNew, setIsNew] = createSignal(false);
  createComputed(() => {
    if (props.volumes !== undefined && named().length === 0 && props.value === "") setIsNew(true);
  });
  const missing = () => props.volumes !== undefined && props.value !== "" && !known() && !isNew();
  const selected = () => (isNew() ? NEW : props.value);

  return (
    <Show
      when={!props.failed}
      fallback={
        <div class="flex flex-col gap-1">
          <label class="flex flex-col gap-1">
            <span class="text-xs">{t("form.volumeName")}</span>
            <Input
              class="input-sm w-56"
              invalid={props.invalid}
              aria-describedby={errorId}
              value={props.value}
              onInput={(e) => props.onChange(e.currentTarget.value)}
            />
          </label>
          <span id={errorId} class="text-xs text-error">
            {t("form.volumesFailed", { message: errorText(props.failed, t) })}
          </span>
        </div>
      }
    >
      <label class="flex flex-col gap-1">
        <span class="text-xs">{t("form.volumeName")}</span>
        <Select
          class="select-sm w-56"
          invalid={props.invalid && !isNew()}
          value={selected()}
          onChange={(e) => {
            const v = e.currentTarget.value;
            setIsNew(v === NEW);
            props.onChange(v === NEW ? "" : v);
          }}
        >
          {/*
            The selected prop of each option, not the value of the select, restores the choice when the options arrive
            late (the list of volumes loads after the form shows): value is set once, options added later do not
            re-apply it. Do not drop them as redundant.
          */}
          <Show when={named().length > 0}>
            <option value="" selected={selected() === ""}>
              {t("form.volumeChoose")}
            </option>
          </Show>
          <For each={named()}>
            {(v) => (
              <option value={v.name} selected={selected() === v.name}>
                {t("form.volumeOption", { name: v.name, driver: v.driver })}
              </option>
            )}
          </For>
          <Show when={missing()}>
            <option value={props.value} selected>
              {t("form.volumeMissing", { name: props.value })}
            </option>
          </Show>
          <option value={NEW} selected={selected() === NEW}>
            {t("form.volumeNew")}
          </option>
        </Select>
      </label>
      <Show when={isNew()}>
        <label class="flex flex-col gap-1">
          <span class="text-xs">{t("form.newVolumeName")}</span>
          <Input
            class="input-sm w-56"
            invalid={props.invalid}
            value={props.value}
            onInput={(e) => props.onChange(e.currentTarget.value)}
          />
        </label>
      </Show>
    </Show>
  );
}
