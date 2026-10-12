import { useExtension, useSession } from "@edelweiss/sdk";
import { onCleanup } from "solid-js";

/**
 * Opens a stream of server-sent events below the extension's API and calls the handler of each named event with its
 * JSON data. The stream closes with the owner; the returned function closes it earlier. A stream the browser gives up
 * on may mean the session expired, so the shell is asked to check it.
 *
 * The browser connects again by itself after a failure, and the server sends no event ids to resume from, so what
 * happened in between is lost. onReopen is called whenever the stream opens again after its first opening (not for
 * the first one): the owner can load what it missed.
 */
export function listen(
  path: string,
  handlers: Record<string, (data: unknown) => void>,
  onReopen?: () => void,
): () => void {
  const ext = useExtension();
  const session = useSession();
  const source = new EventSource(`${ext.apiBase}${path}`);
  for (const [name, handle] of Object.entries(handlers)) {
    source.addEventListener(name, (e) => {
      // The browser reports a failed connection as a plain Event named "error", which reaches the listeners of the
      // streams' own "error" events (logs, statistics). Those are MessageEvents with a string as data; skip the rest.
      const raw = (e as MessageEvent<unknown>).data;
      if (typeof raw !== "string") return;
      let data: unknown;
      try {
        data = JSON.parse(raw);
      } catch {
        return; // a malformed event
      }
      handle(data);
    });
  }
  let opened = false;
  source.onopen = () => {
    if (opened) onReopen?.();
    opened = true;
  };
  source.onerror = () => {
    if (source.readyState === EventSource.CLOSED) void session.refresh().catch(() => {});
  };
  const close = () => source.close();
  onCleanup(close);
  return close;
}

/**
 * Calls onChange, at most every 500 ms, when Docker reports a change of one of the types (container, image, …), and
 * when the stream of events opens again after a loss (the changes of the gap were not delivered).
 */
export function onDockerChange(types: string[], onChange: () => void) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = () => {
    if (timer !== undefined) return;
    timer = setTimeout(() => {
      timer = undefined;
      onChange();
    }, 500);
  };
  listen(
    "/events",
    {
      docker: (data) => {
        const type = (data as { type?: unknown } | null)?.type;
        if (typeof type === "string" && types.includes(type)) schedule();
      },
    },
    schedule,
  );
  onCleanup(() => clearTimeout(timer));
}
