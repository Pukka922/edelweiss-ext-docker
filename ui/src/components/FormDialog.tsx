import { useI18n } from "@edelweiss/sdk";
import { type JSX, onCleanup, onMount, Show } from "solid-js";

/** A label around its control, so the control is found by its label. */
export function Field(props: { label: string; children: JSX.Element }) {
  return (
    // biome-ignore lint/a11y/noLabelWithoutControl: the control is the child, which the rule cannot see
    <label class="flex flex-col gap-1">
      <span class="text-sm">{props.label}</span>
      {props.children}
    </label>
  );
}

/**
 * A modal form: Enter submits, Escape, Cancel and the backdrop cancel (but not while busy: a request is on its way and
 * the dialog stays until it ends), the error of the last attempt shows above the buttons.
 *
 * Focus: on opening it goes to the control marked autofocus, else to the first input, select or textarea; on closing it
 * returns to the element that had it before, if that is still in the document.
 *
 * The Escape listener sits on document, so two dialogs must not be open at the same time (no nesting).
 */
export function FormDialog(props: {
  title: string;
  submitLabel: string;
  busy: boolean;
  error?: string;
  extraActions?: JSX.Element;
  onSubmit: () => void;
  onCancel: () => void;
  children: JSX.Element;
}) {
  const { t } = useI18n();
  let body: HTMLDivElement | undefined;
  let opener: Element | null = null;
  const cancel = () => {
    if (!props.busy) props.onCancel();
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") cancel();
  };
  onMount(() => {
    opener = document.activeElement;
    (
      body?.querySelector<HTMLElement>("[autofocus]") ??
      body?.querySelector<HTMLElement>("input, select, textarea")
    )?.focus();
    document.addEventListener("keydown", onKey);
  });
  onCleanup(() => {
    document.removeEventListener("keydown", onKey);
    if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
  });
  return (
    <div class="modal modal-open" role="dialog" aria-modal="true" aria-label={props.title}>
      <form
        class="modal-box flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (!props.busy) props.onSubmit();
        }}
      >
        <h3 class="text-lg font-bold">{props.title}</h3>
        <div ref={body} class="flex flex-col gap-3">
          {props.children}
        </div>
        <Show when={props.error}>
          {(message) => (
            <div role="alert" class="alert alert-error text-sm">
              {message()}
            </div>
          )}
        </Show>
        <div class="modal-action flex-wrap">
          {props.extraActions}
          <button type="button" class="btn" disabled={props.busy} onClick={cancel}>
            {t("actions.cancel")}
          </button>
          <button type="submit" class="btn btn-primary" disabled={props.busy}>
            {props.submitLabel}
          </button>
        </div>
      </form>
      <div class="modal-backdrop" aria-hidden="true" onClick={cancel} />
    </div>
  );
}
