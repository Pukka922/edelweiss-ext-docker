import { cleanup } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount, settle, stubEventSource } from "../test/mount";
import { StatsTab } from "./StatsTab";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const sample = (mem: number | null) =>
  JSON.stringify({
    time: "2026-10-10T12:00:02Z",
    cpuPercent: 12.5,
    memUsage: mem,
    memLimit: mem === null ? null : 1 << 30,
    netRx: 2048,
    netTx: 1024,
    blkRead: 0,
    blkWrite: 4096,
    pids: 7,
  });

const statsUrl = "/ext/docker/api/containers/web/stats";

describe("StatsTab", () => {
  it("shows the latest values", async () => {
    const sources = stubEventSource();
    const view = mount(() => <StatsTab id="web" running={true} />);
    sources.instances.find((s) => s.url === statsUrl)?.emit("stats", sample(64 << 20));
    expect(await view.findByText("12.5 %")).toBeTruthy();
    expect(view.getByText("64 MiB / 1 GiB")).toBeTruthy();
    expect(view.getByText("in 2 KiB/s · out 1 KiB/s")).toBeTruthy();
    expect(view.getByText("7")).toBeTruthy();
  });

  it("explains missing memory figures", async () => {
    const sources = stubEventSource();
    const view = mount(() => <StatsTab id="web" running={true} />);
    sources.instances[0]?.emit("stats", sample(null));
    expect(await view.findByText(/cgroup_enable=memory/)).toBeTruthy();
    expect(view.getByText("–")).toBeTruthy();
  });

  it("does not stream for a stopped container", () => {
    const sources = stubEventSource();
    const view = mount(() => <StatsTab id="db" running={false} />);
    expect(view.getByText("The container is not running.")).toBeTruthy();
    expect(sources.instances).toHaveLength(0);
  });

  it("says so and stops the stream when the container stops", async () => {
    const sources = stubEventSource();
    const view = mount(() => <StatsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    source.emit("stats", sample(64 << 20));
    await view.findByText("12.5 %");
    source.emit("stopped", "{}");
    expect(await view.findByText("The container is not running.")).toBeTruthy();
    expect(view.queryByText("12.5 %")).toBeNull();
    expect(source.readyState).toBe(2);
  });

  it("shows an error of the stream and stops", async () => {
    const sources = stubEventSource();
    const view = mount(() => <StatsTab id="web" running={true} />);
    const source = sources.instances[0]!;
    source.emit("error", JSON.stringify({ message: "connection reset" }));
    expect(await view.findByText("The statistics stream was interrupted: connection reset")).toBeTruthy();
    expect(view.queryByText("Collecting…")).toBeNull();
    expect(source.readyState).toBe(2);
  });

  it("starts a new stream when the container runs again", async () => {
    const sources = stubEventSource();
    const [running, setRunning] = createSignal(true);
    const view = mount(() => <StatsTab id="web" running={running()} />);
    const first = sources.instances[0]!;
    first.emit("stopped", "{}"); // the container went down: the stream ended
    await view.findByText("The container is not running.");
    setRunning(false); // the detail page reloads and sees it
    await settle();
    expect(sources.instances).toHaveLength(1);
    setRunning(true); // and again after the start
    await settle();
    expect(sources.instances).toHaveLength(2);
    const second = sources.instances[1]!;
    expect(second.url).toBe(statsUrl);
    expect(second.readyState).not.toBe(2);
    expect(view.queryByText("The container is not running.")).toBeNull();
    second.emit("stats", sample(64 << 20));
    expect(await view.findByText("12.5 %")).toBeTruthy();
  });

  it("starts a new stream after an error of the stream when the container runs again", async () => {
    const sources = stubEventSource();
    const [running, setRunning] = createSignal(true);
    const view = mount(() => <StatsTab id="web" running={running()} />);
    sources.instances[0]?.emit("error", JSON.stringify({ message: "boom" }));
    await view.findByText("The statistics stream was interrupted: boom");
    setRunning(false);
    await settle();
    setRunning(true);
    await settle();
    expect(sources.instances).toHaveLength(2);
    expect(view.queryByText("The statistics stream was interrupted: boom")).toBeNull();
    expect(view.getByText("Collecting…")).toBeTruthy();
  });

  it("closes its stream with the tab", () => {
    const sources = stubEventSource();
    mount(() => <StatsTab id="web" running={true} />);
    expect(sources.instances[0]?.readyState).not.toBe(2);
    cleanup();
    expect(sources.instances[0]?.readyState).toBe(2);
  });

  it("closes the stream when the container stops", async () => {
    const sources = stubEventSource();
    const [running, setRunning] = createSignal(true);
    const view = mount(() => <StatsTab id="web" running={running()} />);
    const source = sources.instances[0]!;
    setRunning(false);
    await settle();
    expect(source.readyState).toBe(2);
    expect(view.getByText("The container is not running.")).toBeTruthy();
  });
});
