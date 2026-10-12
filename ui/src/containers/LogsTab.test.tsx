import { cleanup, fireEvent } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount, settle, stubEventSource } from "../test/mount";
import { LogsTab } from "./LogsTab";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const line = (stream: string, text: string) =>
  JSON.stringify({ stream, time: "2026-10-10T12:00:00Z", line: text });

describe("LogsTab", () => {
  it("shows the lines, stderr marked, and the end", async () => {
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances.find((s) => s.url === "/ext/docker/api/containers/web/logs?tail=500")!;
    source.emit("line", line("stdout", "ready"));
    source.emit("line", line("stderr", "oops"));
    expect(await view.findByText("ready")).toBeTruthy();
    expect(view.getByText("oops").classList.contains("text-error")).toBe(true);
    fireEvent.click(view.getByLabelText("Timestamps"));
    expect(view.getAllByText("2026-10-10T12:00:00Z")).toHaveLength(2);
    source.emit("end", "{}");
    expect(await view.findByText("The container has stopped.")).toBeTruthy();
    expect(source.readyState).toBe(2);
  });

  it("holds new lines while paused", async () => {
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    source.emit("line", line("stdout", "one"));
    await view.findByText("one");
    fireEvent.click(view.getByRole("button", { name: "Pause" }));
    source.emit("line", line("stdout", "two"));
    await settle();
    expect(view.queryByText("two")).toBeNull();
    fireEvent.click(view.getByRole("button", { name: "Resume" }));
    expect(await view.findByText("two")).toBeTruthy();
  });

  it("keeps the last 5000 lines, also of a long pause", async () => {
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    fireEvent.click(view.getByRole("button", { name: "Pause" }));
    for (let n = 0; n < 5100; n++) source.emit("line", line("stdout", `l${n}`));
    fireEvent.click(view.getByRole("button", { name: "Resume" }));
    expect(await view.findByText("l5099")).toBeTruthy();
    expect(view.container.querySelectorAll("pre > div")).toHaveLength(5000);
    expect(view.queryByText("l99")).toBeNull();
    expect(view.getByText("l100")).toBeTruthy();
  });

  it("shows the lines of one tick with one update and one scroll, on the next frame", async () => {
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => frames.push(cb));
    vi.stubGlobal("cancelAnimationFrame", () => {});
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    const box = view.container.querySelector("pre")!;
    let scrolls = 0;
    Object.defineProperty(box, "scrollTop", { configurable: true, get: () => 0, set: () => scrolls++ });
    const rows = () => box.querySelectorAll(":scope > div").length;
    for (let n = 0; n < 1000; n++) source.emit("line", line("stdout", `l${n}`));
    expect(frames).toHaveLength(1);
    expect(rows()).toBe(0);
    frames.shift()!(0);
    expect(rows()).toBe(1000);
    expect(scrolls).toBe(1);

    // the next lines wait for the next frame, and the view keeps the last 5000
    for (let n = 1000; n < 12000; n++) source.emit("line", line("stdout", `l${n}`));
    expect(frames).toHaveLength(1);
    frames.shift()!(0);
    expect(rows()).toBe(5000);
    expect(scrolls).toBe(2);
    expect(view.queryByText("l6999")).toBeNull();
    expect(view.getByText("l7000")).toBeTruthy();
    expect(view.getByText("l11999")).toBeTruthy();
    await settle();
    expect(frames).toHaveLength(0);
  });

  it("falls back to a timer without animation frames", async () => {
    vi.stubGlobal("requestAnimationFrame", undefined);
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    source.emit("line", line("stdout", "one"));
    source.emit("line", line("stdout", "two"));
    await settle(50);
    expect(view.queryByText("one")).toBeNull();
    await settle(80);
    expect(view.getByText("one")).toBeTruthy();
    expect(view.getByText("two")).toBeTruthy();
  });

  it("keeps the last 5000 lines of a very long pause", async () => {
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    fireEvent.click(view.getByRole("button", { name: "Pause" }));
    for (let n = 0; n < 23456; n++) source.emit("line", line("stdout", `l${n}`));
    await settle();
    expect(view.container.querySelectorAll("pre > div")).toHaveLength(0);
    fireEvent.click(view.getByRole("button", { name: "Resume" }));
    expect(view.container.querySelectorAll("pre > div")).toHaveLength(5000);
    expect(view.queryByText("l18455")).toBeNull();
    expect(view.getByText("l18456")).toBeTruthy();
    expect(view.getByText("l23455")).toBeTruthy();
  });

  it("closes its stream with the tab", () => {
    const sources = stubEventSource();
    mount(() => <LogsTab id="web" running={true} />);
    expect(sources.instances[0]?.readyState).not.toBe(2);
    cleanup();
    expect(sources.instances[0]?.readyState).toBe(2);
  });

  it("shows an error of the stream and stops", async () => {
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="gone" running={true} />);
    const source = sources.instances[0]!;
    source.emit("error", JSON.stringify({ message: "No such container: gone" }));
    expect(await view.findByText("The log stream was interrupted: No such container: gone")).toBeTruthy();
    expect(source.readyState).toBe(2);
  });

  it("reads the log of a stopped container, and starts again when the container does", async () => {
    const sources = stubEventSource();
    const [running, setRunning] = createSignal(false);
    const view = mount(() => <LogsTab id="web" running={running()} />);
    const first = sources.instances[0]!;
    first.emit("line", line("stdout", "old"));
    first.emit("end", "{}");
    expect(await view.findByText("The container has stopped.")).toBeTruthy();
    expect(view.getByText("old")).toBeTruthy();
    expect(sources.instances).toHaveLength(1);
    setRunning(true);
    await settle();
    expect(sources.instances).toHaveLength(2);
    const second = sources.instances[1]!;
    expect(second.url).toBe(first.url);
    // the new stream starts with the tail again: the old lines and the end are gone
    expect(view.queryByText("old")).toBeNull();
    expect(view.queryByText("The container has stopped.")).toBeNull();
    second.emit("line", line("stdout", "new"));
    expect(await view.findByText("new")).toBeTruthy();
  });

  it("starts a new stream when the container runs again, also after an error", async () => {
    const sources = stubEventSource();
    const [running, setRunning] = createSignal(true);
    const view = mount(() => <LogsTab id="web" running={running()} />);
    sources.instances[0]?.emit("error", JSON.stringify({ message: "boom" }));
    await view.findByText("The log stream was interrupted: boom");
    setRunning(false);
    await settle();
    expect(sources.instances).toHaveLength(1);
    setRunning(true);
    await settle();
    expect(sources.instances).toHaveLength(2);
    expect(sources.instances[0]?.readyState).toBe(2);
    expect(sources.instances[1]?.readyState).not.toBe(2);
    expect(view.queryByText("The log stream was interrupted: boom")).toBeNull();
  });

  it("does not open a new stream while the container stays up", async () => {
    const sources = stubEventSource();
    const [running, setRunning] = createSignal(true);
    mount(() => <LogsTab id="web" running={running()} />);
    setRunning(true);
    await settle();
    expect(sources.instances).toHaveLength(1);
  });

  it("clears the lines when the stream opens again: the server sends the tail again", async () => {
    const sources = stubEventSource();
    const view = mount(() => <LogsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    source.open();
    source.emit("line", line("stdout", "before"));
    await view.findByText("before");
    source.fail(false); // the connection broke; the browser connects again by itself
    source.open();
    source.emit("line", line("stdout", "before"));
    source.emit("line", line("stdout", "after"));
    expect(await view.findByText("after")).toBeTruthy();
    expect(view.getAllByText("before")).toHaveLength(1);
  });
});
