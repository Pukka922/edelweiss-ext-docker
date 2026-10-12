import { useExtension, useI18n } from "@edelweiss/sdk";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import { createSignal, onCleanup, Show } from "solid-js";

type State = "idle" | "connecting" | "open" | "closed";

// The backend ends the session when a frame is larger than 1 MiB (close code 1009), and a paste can be larger.
const MAX_FRAME = 64 * 1024;

/** The bytes of the input in frames of at most MAX_FRAME (the order is kept, a character may span two frames). */
function frames(input: string): Uint8Array<ArrayBuffer>[] {
  const bytes = new TextEncoder().encode(input);
  const out: Uint8Array<ArrayBuffer>[] = [];
  for (let at = 0; at < bytes.length; at += MAX_FRAME) out.push(bytes.subarray(at, at + MAX_FRAME));
  return out;
}

function Console(props: { id: string }) {
  const ext = useExtension();
  const { t } = useI18n();
  const [cmd, setCmd] = createSignal("/bin/sh");
  const [state, setState] = createSignal<State>("idle");
  const [message, setMessage] = createSignal<string>();
  let el: HTMLDivElement | undefined;
  let term: Terminal | undefined;
  let socket: WebSocket | undefined;
  let observer: ResizeObserver | undefined;

  const disconnect = () => {
    if (socket) {
      // a closing socket still reports its end and late output: that concerns a console that is gone
      socket.onopen = null;
      socket.onmessage = null;
      socket.onclose = null;
      socket.close();
      socket = undefined;
    }
    observer?.disconnect();
    observer = undefined;
    term?.dispose();
    term = undefined;
  };

  const connect = () => {
    if (!el) return;
    disconnect();
    setMessage(undefined);
    const terminal = new Terminal({ cursorBlink: true, fontFamily: "ui-monospace, monospace", fontSize: 14 });
    const fit = new FitAddon();
    terminal.loadAddon(fit);
    terminal.open(el);
    fit.fit();
    term = terminal;
    const scheme = window.location.protocol === "https:" ? "wss:" : "ws:";
    // without a command the backend starts /bin/sh (an empty cmd is an error there)
    const command = cmd().trim();
    const query = command === "" ? "" : `?cmd=${encodeURIComponent(command)}`;
    const ws = new WebSocket(
      `${scheme}//${window.location.host}${ext.apiBase}/containers/${encodeURIComponent(props.id)}/console${query}`,
    );
    ws.binaryType = "arraybuffer";
    socket = ws;
    setState("connecting");
    const sendSize = () => {
      if (ws.readyState === WebSocket.OPEN)
        ws.send(JSON.stringify({ type: "resize", cols: terminal.cols, rows: terminal.rows }));
    };
    ws.onopen = () => {
      setState("open");
      sendSize();
      terminal.focus();
    };
    ws.onmessage = (e: MessageEvent) => {
      if (typeof e.data === "string") {
        const msg = JSON.parse(e.data) as { type: string; code?: number; message?: string };
        if (msg.type === "exit") setMessage(t("console.exited", { code: msg.code ?? -1 }));
        if (msg.type === "error") setMessage(t("console.error", { message: msg.message ?? "" }));
        return;
      }
      terminal.write(new Uint8Array(e.data as ArrayBuffer));
    };
    ws.onclose = () => setState("closed");
    terminal.onData((d) => {
      if (ws.readyState !== WebSocket.OPEN) return;
      for (const frame of frames(d)) ws.send(frame);
    });
    terminal.onResize(sendSize);
    observer = new ResizeObserver(() => fit.fit());
    observer.observe(el);
  };
  onCleanup(disconnect);

  const connected = () => state() === "connecting" || state() === "open";
  return (
    <div class="flex flex-col gap-2">
      <form
        class="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (connected()) {
            disconnect();
            setState("closed");
          } else connect();
        }}
      >
        <label class="flex flex-col gap-1">
          <span class="text-sm">{t("console.command")}</span>
          <input
            class="input input-sm w-64 font-mono"
            value={cmd()}
            disabled={connected()}
            onInput={(e) => setCmd(e.currentTarget.value)}
          />
        </label>
        <button type="submit" class="btn btn-sm">
          {connected() ? t("console.disconnect") : t("console.connect")}
        </button>
        <span class="badge badge-ghost">{t(`console.states.${state()}`)}</span>
      </form>
      <Show when={message()}>
        <p class="text-sm text-base-content/70">{message()}</p>
      </Show>
      <div ref={el} class="h-96 w-full overflow-hidden rounded-box bg-black p-1" />
    </div>
  );
}

/** A terminal in the container (docker exec with a TTY); it needs a running container. */
export function ConsoleTab(props: { id: string; running: boolean }) {
  const { t } = useI18n();
  return (
    <Show when={props.running} fallback={<p class="text-base-content/70">{t("console.notRunning")}</p>}>
      <Console id={props.id} />
    </Show>
  );
}
