import { cleanup, fireEvent } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { mount, settle } from "../test/mount";
import { ConsoleTab } from "./ConsoleTab";

// xterm needs a real layout; the test checks the wiring with a fake terminal (vi.hoisted: vi.mock runs first)
const { terminals, FakeTerminal } = vi.hoisted(() => {
  class FakeTerminal {
    cols = 80;
    rows = 24;
    written: string[] = [];
    disposed = false;
    data?: (d: string) => void;
    resize?: () => void;
    constructor() {
      terminals.push(this);
    }
    loadAddon() {}
    open() {}
    focus() {}
    dispose() {
      this.disposed = true;
    }
    write(d: string | Uint8Array) {
      this.written.push(typeof d === "string" ? d : new TextDecoder().decode(d));
    }
    onData(cb: (d: string) => void) {
      this.data = cb;
      return { dispose() {} };
    }
    onResize(cb: () => void) {
      this.resize = cb;
      return { dispose() {} };
    }
  }
  const terminals: FakeTerminal[] = [];
  return { terminals, FakeTerminal };
});
vi.mock("@xterm/xterm", () => ({ Terminal: FakeTerminal }));
vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));

class FakeSocket {
  static instances: FakeSocket[] = [];
  static OPEN = 1;
  readyState = 0;
  binaryType = "blob";
  sent: (string | Uint8Array)[] = [];
  onopen?: (() => void) | null;
  onmessage?: ((e: { data: unknown }) => void) | null;
  onclose?: (() => void) | null;
  constructor(readonly url: string) {
    FakeSocket.instances.push(this);
  }
  send(d: string | Uint8Array) {
    this.sent.push(d);
  }
  close() {
    this.readyState = 3;
    this.onclose?.();
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
}

const base = `ws://${window.location.host}/ext/docker/api/containers/web/console`;

beforeEach(() => {
  FakeSocket.instances = [];
  terminals.length = 0;
  vi.stubGlobal("WebSocket", FakeSocket);
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** Connects with the command typed into the field (the default of the field if cmd is undefined). */
function connect(view: ReturnType<typeof mount>, cmd?: string) {
  if (cmd !== undefined) fireEvent.input(view.getByLabelText("Command"), { target: { value: cmd } });
  fireEvent.click(view.getByRole("button", { name: "Connect" }));
  return FakeSocket.instances[FakeSocket.instances.length - 1]!;
}

describe("ConsoleTab", () => {
  it("connects the terminal to the container", async () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    const ws = connect(view, "/bin/bash");
    expect(ws.url).toBe(`${base}?cmd=%2Fbin%2Fbash`);
    expect(ws.binaryType).toBe("arraybuffer");
    ws.open();
    expect(ws.sent[0]).toBe(JSON.stringify({ type: "resize", cols: 80, rows: 24 }));
    ws.onmessage?.({ data: new TextEncoder().encode("$ ").buffer });
    expect(terminals[0]?.written).toEqual(["$ "]);
    terminals[0]?.data?.("ls\r");
    expect(new TextDecoder().decode(ws.sent[1] as Uint8Array)).toBe("ls\r");
    ws.onmessage?.({ data: JSON.stringify({ type: "exit", code: 0 }) });
    expect(await view.findByText("The command ended with exit code 0.")).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Disconnect" }));
    await settle();
    expect(ws.readyState).toBe(3);
  });

  it("does not connect to a stopped container", () => {
    const view = mount(() => <ConsoleTab id="db" running={false} />);
    expect(view.getByText("The container is not running.")).toBeTruthy();
    expect(view.queryByRole("button", { name: "Connect" })).toBeNull();
  });

  it("starts the shell of the image by default: the field starts with /bin/sh", () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    expect(connect(view).url).toBe(`${base}?cmd=%2Fbin%2Fsh`);
  });

  it("leaves out the command when the field is empty, so the backend picks its default", () => {
    for (const empty of ["", "   "]) {
      const view = mount(() => <ConsoleTab id="web" running={true} />);
      expect(connect(view, empty).url).toBe(base);
      cleanup();
    }
  });

  it("sends the input in frames of at most 64 KiB, in order and without loss", () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    const ws = connect(view);
    ws.open();
    const paste = "ä€x".repeat(60_000); // 6 bytes per repeat, 360 000 bytes
    terminals[0]?.data?.(paste);
    const frames = ws.sent.slice(1) as Uint8Array[];
    expect(frames.length).toBeGreaterThan(1);
    for (const f of frames) {
      expect(ArrayBuffer.isView(f)).toBe(true);
      expect(f.byteLength).toBeLessThanOrEqual(64 * 1024);
    }
    expect(frames.slice(0, -1).every((f) => f.byteLength === 64 * 1024)).toBe(true);
    const all = new Uint8Array(frames.reduce((n, f) => n + f.byteLength, 0));
    let at = 0;
    for (const f of frames) {
      all.set(f, at);
      at += f.byteLength;
    }
    expect(new TextDecoder().decode(all)).toBe(paste);
  });

  it("sends a small input as one frame, and nothing for an empty one", () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    const ws = connect(view);
    ws.open();
    terminals[0]?.data?.("");
    terminals[0]?.data?.("a");
    expect(ws.sent).toHaveLength(2); // the size, then "a"
  });

  it("sends nothing while the socket is not open, and the size when the terminal changes", () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    const ws = connect(view);
    terminals[0]?.data?.("early");
    expect(ws.sent).toHaveLength(0);
    ws.open();
    const term = terminals[0]!;
    term.cols = 100;
    term.rows = 30;
    term.resize?.();
    expect(ws.sent[ws.sent.length - 1]).toBe(JSON.stringify({ type: "resize", cols: 100, rows: 30 }));
  });

  it("shows why the command could not start, and the state when the socket closes", async () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    const ws = connect(view, "ls '");
    ws.open();
    expect(await view.findByText("connected")).toBeTruthy();
    ws.onmessage?.({ data: JSON.stringify({ type: "error", message: "the command is invalid" }) });
    expect(await view.findByText("The console could not start: the command is invalid")).toBeTruthy();
    ws.close();
    expect(await view.findByText("disconnected")).toBeTruthy();
    expect(view.getByRole("button", { name: "Connect" })).toBeTruthy();
  });

  it("builds a new terminal for the next connection", async () => {
    const view = mount(() => <ConsoleTab id="web" running={true} />);
    const first = connect(view);
    first.open();
    fireEvent.click(view.getByRole("button", { name: "Disconnect" }));
    await settle();
    expect(terminals[0]?.disposed).toBe(true);
    // what the closed socket still delivers is not written to the terminal that is gone
    first.onmessage?.({ data: new TextEncoder().encode("late").buffer });
    expect(terminals[0]?.written).toEqual([]);
    const second = connect(view);
    expect(second).not.toBe(first);
    expect(terminals).toHaveLength(2);
    // nor does the end of the first socket mark the second connection as closed
    first.onclose?.();
    second.open();
    expect(await view.findByText("connected")).toBeTruthy();
  });

  it("closes the socket and the terminal when the container stops", async () => {
    const [running, setRunning] = createSignal(true);
    const view = mount(() => <ConsoleTab id="web" running={running()} />);
    const ws = connect(view);
    ws.open();
    setRunning(false);
    await settle();
    expect(ws.readyState).toBe(3);
    expect(terminals[0]?.disposed).toBe(true);
    expect(view.getByText("The container is not running.")).toBeTruthy();
    // after the next start the tab is fresh: not connected, ready for a new connection
    setRunning(true);
    await settle();
    expect(view.getByText("not connected")).toBeTruthy();
    expect(view.getByRole("button", { name: "Connect" })).toBeTruthy();
  });
});
