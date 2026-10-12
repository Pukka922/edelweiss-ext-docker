import { cleanup } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { listen, onDockerChange } from "./live";
import { mount, stubEventSource } from "./test/mount";

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/** Mounts a page that follows the container and image events, and returns the stream it opened. */
function follow(onChange: () => void) {
  const sources = stubEventSource();
  mount(() => {
    onDockerChange(["container", "image"], onChange);
    return null;
  });
  const stream = sources.instances[0];
  if (!stream) throw new Error("the events stream was not opened");
  return stream;
}

describe("onDockerChange", () => {
  it("opens GET /events below the extension's API", () => {
    const stream = follow(() => {});
    expect(stream.url).toBe("/ext/docker/api/events");
  });

  it("calls onChange once, 500 ms after a docker event of a listed type, however many arrive", () => {
    const onChange = vi.fn();
    const stream = follow(onChange);
    stream.open();
    stream.emit("docker", JSON.stringify({ type: "container", action: "start", id: "a" }));
    stream.emit("docker", JSON.stringify({ type: "image", action: "pull", id: "b" }));
    vi.advanceTimersByTime(499);
    expect(onChange).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(onChange).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(2000);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("ignores events of other types and malformed ones", () => {
    const onChange = vi.fn();
    const stream = follow(onChange);
    stream.open();
    stream.emit("docker", JSON.stringify({ type: "network", action: "create", id: "n" }));
    stream.emit("docker", JSON.stringify({ action: "start" }));
    stream.emit("docker", "not json");
    vi.advanceTimersByTime(1000);
    expect(onChange).not.toHaveBeenCalled();
  });

  it("does not call onChange when the stream opens the first time, but once (debounced) when it opens again", () => {
    const onChange = vi.fn();
    const stream = follow(onChange);
    stream.open();
    vi.advanceTimersByTime(1000);
    expect(onChange).not.toHaveBeenCalled();
    // the browser reconnected: events of the gap are lost, so the page loads its data again
    stream.fail(false);
    stream.open();
    vi.advanceTimersByTime(499);
    expect(onChange).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("does not call onChange after its owner is gone", () => {
    const onChange = vi.fn();
    const stream = follow(onChange);
    stream.open();
    stream.emit("docker", JSON.stringify({ type: "container", action: "die", id: "a" }));
    cleanup();
    vi.advanceTimersByTime(1000);
    expect(onChange).not.toHaveBeenCalled();
    expect(stream.readyState).toBe(2); // closed
  });
});

describe("listen", () => {
  it("hands the JSON of an event to its handler", () => {
    const handler = vi.fn();
    const sources = stubEventSource();
    mount(() => {
      listen("/containers/web/stats", { stats: handler });
      return null;
    });
    const stream = sources.instances[0];
    expect(stream?.url).toBe("/ext/docker/api/containers/web/stats");
    stream?.emit("stats", JSON.stringify({ cpuPercent: 1.5 }));
    stream?.emit("stats", "not json");
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith({ cpuPercent: 1.5 });
  });

  it("tells the error event of a stream from the connection error of the browser, which has no data", () => {
    const handler = vi.fn();
    const sources = stubEventSource();
    mount(() => {
      listen("/containers/web/logs", { error: handler });
      return null;
    });
    const stream = sources.instances[0];
    if (!stream) throw new Error("the stream was not opened");
    // A real EventSource also calls the listeners of "error" for its own failed connection, with a plain Event (the
    // fake only calls onerror for that, so the listener is called by hand). A MessageEvent without data has the data
    // null, which JSON.parse would turn into a null for the handler.
    type Listeners = Map<string, ((e: Event) => void)[]>;
    const listeners = (stream as unknown as { listeners: Listeners }).listeners.get("error") ?? [];
    expect(listeners).toHaveLength(1);
    for (const event of [new Event("error"), new MessageEvent("error")]) listeners[0]?.(event);
    expect(handler).not.toHaveBeenCalled();
    stream.emit("error", JSON.stringify({ message: "Error grabbing logs" }));
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler).toHaveBeenCalledWith({ message: "Error grabbing logs" });
  });
});
