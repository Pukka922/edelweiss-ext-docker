import { useI18n } from "@edelweiss/sdk";
import { createSignal, For, onCleanup, onMount } from "solid-js";
import type { Layer, PullProgress as Progress } from "../api";
import { listen } from "../live";

/** The overall progress (0–100) of the layers whose size is known, weighted by size. */
export function overall(layers: Layer[]): number {
  const sized = layers.filter((l) => l.total > 0);
  const total = sized.reduce((sum, l) => sum + l.total, 0);
  if (total === 0) return 0;
  const done = sized.reduce((sum, l) => sum + Math.min(l.current, l.total), 0);
  return Math.round((done / total) * 100);
}

/**
 * A modal that follows the pull of image (a reference like nginx:1.29); it reports the end once and closes its stream
 * then. The browser reports a stream that is lost for good (the job is gone, the session ended) to nobody, so the user
 * can always leave: the Close button and Escape call onClose, after which neither onDone nor onError is called. The pull
 * itself goes on in the backend.
 */
export function PullProgress(props: {
  id: string;
  image: string;
  onDone: () => void;
  onError: (message: string) => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [progress, setProgress] = createSignal<Progress>();
  let finished = false;
  const stop = listen(`/pulls/${encodeURIComponent(props.id)}/events`, {
    progress: (data) => setProgress(data as Progress),
    done: () => {
      if (finished) return;
      finished = true;
      stop();
      props.onDone();
    },
    error: (data) => {
      if (finished) return;
      finished = true;
      stop();
      props.onError(String((data as { message?: unknown }).message ?? ""));
    },
  });
  const close = () => {
    if (finished) return;
    finished = true;
    stop();
    props.onClose();
  };
  let closeButton: HTMLButtonElement | undefined;
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") close();
  };
  onMount(() => {
    closeButton?.focus();
    document.addEventListener("keydown", onKey);
  });
  onCleanup(() => document.removeEventListener("keydown", onKey));
  const title = () => t("pull.title", { ref: props.image });
  return (
    <div class="modal modal-open" role="dialog" aria-modal="true" aria-label={title()}>
      <div class="modal-box">
        <h3 class="text-lg font-bold">{title()}</h3>
        <p class="py-2 text-sm">{progress()?.status || t("pull.starting")}</p>
        <progress
          class="progress progress-primary w-full"
          value={overall(progress()?.layers ?? [])}
          max="100"
        />
        <ul class="mt-2 max-h-60 overflow-y-auto font-mono text-xs">
          <For each={progress()?.layers ?? []}>
            {(l) => (
              <li>{`${l.id}: ${l.status}${l.total > 0 ? ` ${Math.round((Math.min(l.current, l.total) / l.total) * 100)} %` : ""}`}</li>
            )}
          </For>
        </ul>
        <p class="mt-3 text-xs text-base-content/70">{t("pull.background")}</p>
        <div class="modal-action">
          <button ref={closeButton} type="button" class="btn" onClick={close}>
            {t("actions.close")}
          </button>
        </div>
      </div>
    </div>
  );
}
