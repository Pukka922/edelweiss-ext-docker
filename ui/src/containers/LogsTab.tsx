import { useI18n } from "@edelweiss/sdk";
import { createEffect, createSignal, For, on, onCleanup, Show } from "solid-js";
import type { LogLine, StreamError } from "../api";
import { listen } from "../live";

const MAX_LINES = 5000;

/** Calls fn once on the next animation frame, or after 100 ms where there are none; returns the cancel function. */
function onNextFrame(fn: () => void): () => void {
  if (typeof requestAnimationFrame === "function") {
    const id = requestAnimationFrame(() => fn());
    return () => cancelAnimationFrame(id);
  }
  const id = setTimeout(fn, 100);
  return () => clearTimeout(id);
}

/** Drops the oldest lines of buf once it holds twice the view's lines: a batch at a time, not one per line. */
function trim(buf: LogLine[]) {
  if (buf.length >= 2 * MAX_LINES) buf.splice(0, buf.length - MAX_LINES);
}

function Logs(props: { id: string }) {
  const { t } = useI18n();
  const [lines, setLines] = createSignal<LogLine[]>([]);
  const [paused, setPaused] = createSignal(false);
  const [follow, setFollow] = createSignal(true);
  const [timestamps, setTimestamps] = createSignal(false);
  const [ended, setEnded] = createSignal(false);
  const [failed, setFailed] = createSignal<string>();
  // the lines arrive one event each, often thousands at once: they wait in pending and go into the view once per frame
  let pending: LogLine[] = [];
  let held: LogLine[] = []; // the lines that arrived while paused
  let cancelFlush: (() => void) | undefined;
  let box: HTMLPreElement | undefined;
  const flush = () => {
    cancelFlush?.();
    cancelFlush = undefined;
    if (pending.length === 0) return;
    const more = pending;
    pending = [];
    setLines((prev) => {
      const all = prev.concat(more);
      return all.length > MAX_LINES ? all.slice(-MAX_LINES) : all;
    });
  };
  onCleanup(() => cancelFlush?.());
  const close = listen(
    `/containers/${encodeURIComponent(props.id)}/logs?tail=500`,
    {
      line: (data) => {
        const l = data as LogLine;
        if (paused()) {
          held.push(l);
          trim(held); // a long pause keeps no more than the view does
          return;
        }
        pending.push(l);
        trim(pending);
        cancelFlush ??= onNextFrame(flush);
      },
      end: () => {
        flush();
        setEnded(true);
        close(); // EventSource would reconnect and send the last lines again
      },
      error: (data) => {
        flush();
        setFailed((data as Partial<StreamError>).message ?? "");
        close();
      },
    },
    // the browser connected again after a loss: the server starts with the last lines again
    () => {
      held = [];
      pending = [];
      setLines([]);
    },
  );
  const togglePause = () => {
    if (paused()) {
      pending = pending.concat(held);
      held = [];
      flush();
    }
    setPaused(!paused());
  };
  // once per update of the view, so once per flush
  createEffect(
    on(lines, () => {
      if (follow() && box) box.scrollTop = box.scrollHeight;
    }),
  );
  return (
    <div class="flex flex-col gap-2">
      <div class="flex flex-wrap items-center gap-3">
        <button type="button" class="btn btn-sm" onClick={togglePause}>
          {paused() ? t("logs.resume") : t("logs.pause")}
        </button>
        <label class="flex items-center gap-1 text-sm">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked={timestamps()}
            onChange={(e) => setTimestamps(e.currentTarget.checked)}
          />
          {t("logs.timestamps")}
        </label>
        <label class="flex items-center gap-1 text-sm">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked={follow()}
            onChange={(e) => setFollow(e.currentTarget.checked)}
          />
          {t("logs.follow")}
        </label>
      </div>
      <pre ref={box} class="h-96 overflow-auto rounded-box bg-base-200 p-2 text-xs leading-5">
        <Show
          when={lines().length > 0}
          fallback={<span class="text-base-content/50">{t("logs.empty")}</span>}
        >
          <For each={lines()}>
            {(l) => (
              <div>
                <Show when={timestamps()}>
                  <span class="mr-2 text-base-content/50">{l.time}</span>
                </Show>
                <span classList={{ "text-error": l.stream === "stderr" }}>{l.line}</span>
              </div>
            )}
          </For>
        </Show>
      </pre>
      <Show when={ended()}>
        <p class="text-sm text-base-content/70">{t("logs.ended")}</p>
      </Show>
      <Show when={failed() !== undefined}>
        <p role="alert" class="text-sm text-error">
          {t("logs.error", { message: failed() ?? "" })}
        </p>
      </Show>
    </div>
  );
}

/**
 * The log of a container, followed live. The stream ends with the container (and on an error); it reads the log of a
 * stopped container as well. When the container runs again (running goes from false to true) the tab opens a new
 * stream, which starts with the last lines again.
 */
export function LogsTab(props: { id: string; running: boolean }) {
  const [starts, setStarts] = createSignal(0);
  let wasRunning = props.running;
  createEffect(() => {
    const running = props.running;
    if (running && !wasRunning) setStarts((n) => n + 1);
    wasRunning = running;
  });
  // a new key builds Logs again: a new stream, and no lines, end or error of the old one
  return <For each={[starts()]}>{() => <Logs id={props.id} />}</For>;
}
