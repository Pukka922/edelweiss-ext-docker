import { useI18n } from "@edelweiss/sdk";
import { createSignal, onCleanup, onMount, Show } from "solid-js";

type Target = { id: string; name: string };

function Prompt(props: { target: Target; onCancel: () => void; onConfirm: (withVolumes: boolean) => void }) {
  const { t } = useI18n();
  const [withVolumes, setWithVolumes] = createSignal(false);
  let cancel: HTMLButtonElement | undefined;
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") props.onCancel();
  };
  onMount(() => {
    cancel?.focus();
    document.addEventListener("keydown", onKey);
  });
  onCleanup(() => document.removeEventListener("keydown", onKey));
  return (
    <div class="modal modal-open" role="dialog" aria-modal="true" aria-label={t("containers.deleteTitle")}>
      <div class="modal-box">
        <h3 class="text-lg font-bold">{t("containers.deleteTitle")}</h3>
        <p class="py-4">{t("containers.deleteMessage", { name: props.target.name })}</p>
        <label class="flex items-center gap-2">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked={withVolumes()}
            onChange={(e) => setWithVolumes(e.currentTarget.checked)}
          />
          <span>{t("containers.deleteVolumes")}</span>
        </label>
        <div class="modal-action">
          <button ref={cancel} type="button" class="btn" onClick={() => props.onCancel()}>
            {t("actions.cancel")}
          </button>
          <button type="button" class="btn btn-error" onClick={() => props.onConfirm(withVolumes())}>
            {t("actions.delete")}
          </button>
        </div>
      </div>
      <div class="modal-backdrop" aria-hidden="true" onClick={() => props.onCancel()} />
    </div>
  );
}

/** Asks before deleting a container, with the choice to delete its anonymous volumes too. */
export function DeleteContainerDialog(props: {
  target?: Target;
  onCancel: () => void;
  onConfirm: (withVolumes: boolean) => void;
}) {
  return (
    <Show when={props.target} keyed>
      {(target) => <Prompt target={target} onCancel={props.onCancel} onConfirm={props.onConfirm} />}
    </Show>
  );
}
