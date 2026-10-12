import { cleanup, fireEvent } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount, settle, stubEventSource } from "../test/mount";
import { overall, PullProgress } from "./PullProgress";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("overall", () => {
  it("weighs the layers by size and ignores layers without one", () => {
    expect(overall([])).toBe(0);
    expect(
      overall([
        { id: "a", status: "Downloading", current: 50, total: 100 },
        { id: "b", status: "Waiting", current: 0, total: 0 },
      ]),
    ).toBe(50);
    expect(
      overall([
        { id: "a", status: "x", current: 100, total: 100 },
        { id: "b", status: "x", current: 0, total: 300 },
      ]),
    ).toBe(25);
  });
});

describe("PullProgress", () => {
  it("shows the progress and reports the end once", async () => {
    const sources = stubEventSource();
    const onDone = vi.fn();
    const onError = vi.fn();
    const view = mount(() => (
      <PullProgress id="p1" image="nginx:1.29" onDone={onDone} onError={onError} onClose={() => {}} />
    ));
    const source = sources.instances.find((s) => s.url === "/ext/docker/api/pulls/p1/events")!;
    source.emit(
      "progress",
      JSON.stringify({
        ref: "nginx:1.29",
        status: "Pulling from library/nginx",
        done: false,
        layers: [{ id: "aaaa", status: "Downloading", current: 30, total: 100 }],
      }),
    );
    expect(await view.findByText("Pulling from library/nginx")).toBeTruthy();
    expect(view.getByText("aaaa: Downloading 30 %")).toBeTruthy();
    source.emit("done", JSON.stringify({ ref: "nginx:1.29" }));
    source.emit("done", JSON.stringify({ ref: "nginx:1.29" }));
    await settle();
    expect(onDone).toHaveBeenCalledTimes(1);
    expect(source.readyState).toBe(2); // closed, so EventSource does not reconnect and replay the pull
    expect(onError).not.toHaveBeenCalled();
  });

  it("reports an error", async () => {
    const sources = stubEventSource();
    const onError = vi.fn();
    mount(() => <PullProgress id="p2" image="x:1" onDone={() => {}} onError={onError} onClose={() => {}} />);
    sources.instances[0]?.emit("error", JSON.stringify({ message: "pull access denied" }));
    await settle();
    expect(onError).toHaveBeenCalledWith("pull access denied");
  });

  describe("leaving", () => {
    function open() {
      const sources = stubEventSource();
      const onDone = vi.fn();
      const onError = vi.fn();
      const onClose = vi.fn();
      const view = mount(() => (
        <PullProgress id="p3" image="nginx:1.29" onDone={onDone} onError={onError} onClose={onClose} />
      ));
      const source = sources.instances.find((s) => s.url === "/ext/docker/api/pulls/p3/events")!;
      return { view, source, onDone, onError, onClose };
    }

    it("moves the focus into the dialog and says that the pull goes on", () => {
      const { view } = open();
      expect(document.activeElement).toBe(view.getByRole("button", { name: "Close" }));
      expect(view.getByText("The pull continues in the background.")).toBeTruthy();
    });

    it("closes with the Close button, which does not submit a form around it", async () => {
      const { view, source, onDone, onError, onClose } = open();
      const button = view.getByRole("button", { name: "Close" }) as HTMLButtonElement;
      expect(button.type).toBe("button");
      fireEvent.click(button);
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(source.readyState).toBe(2);
      // what the stream still says after that is of no interest
      source.emit("done", JSON.stringify({ ref: "nginx:1.29" }));
      source.emit("error", JSON.stringify({ message: "late" }));
      await settle();
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(onDone).not.toHaveBeenCalled();
      expect(onError).not.toHaveBeenCalled();
    });

    it("closes with Escape", () => {
      const { source, onClose } = open();
      fireEvent.keyDown(document, { key: "Enter" });
      expect(onClose).not.toHaveBeenCalled();
      fireEvent.keyDown(document, { key: "Escape" });
      expect(onClose).toHaveBeenCalledTimes(1);
      expect(source.readyState).toBe(2);
    });

    it("does not ask to close after the end of the pull", async () => {
      const { view, source, onDone, onClose } = open();
      source.emit("done", JSON.stringify({ ref: "nginx:1.29" }));
      await settle();
      fireEvent.keyDown(document, { key: "Escape" });
      fireEvent.click(view.getByRole("button", { name: "Close" }));
      expect(onDone).toHaveBeenCalledTimes(1);
      expect(onClose).not.toHaveBeenCalled();
    });
  });
});
