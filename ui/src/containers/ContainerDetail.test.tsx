import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { createSignal, Show } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { info } from "../test/fixtures";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { ContainerDetail } from "./ContainerDetail";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** A response the test finishes by hand. */
function pending() {
  let finish!: () => void;
  const response = new Promise<Response>((resolve) => {
    finish = () => resolve(new Response(null, { status: 204 }));
  });
  return { response, finish };
}

/** The container of the fixture in another state. */
function withState(status: string, running: boolean) {
  return { ...info, container: { ...info.container, state: { ...info.container.state, status, running } } };
}

describe("ContainerDetail", () => {
  it("shows the overview", async () => {
    stubEventSource();
    stubFetch({ "GET /containers/web": () => json(200, info) });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    expect(await view.findByRole("heading", { name: "web" })).toBeTruthy();
    expect(view.getByText("127.0.0.1:8080 → 80/tcp")).toBeTruthy();
    expect(view.getByText("web-data → /data")).toBeTruthy();
    expect(view.getByText("/srv/conf → /etc/nginx/conf.d (read-only)")).toBeTruthy();
    expect(view.getByText("bridge: 172.17.0.2")).toBeTruthy();
    expect(view.getByText("TZ=UTC")).toBeTruthy();
    expect(view.getByText("unless stopped")).toBeTruthy();
    expect(view.queryByText("none")).toBeNull();
  });

  it("says none for ports, volumes, networks and variables a container does not have", async () => {
    stubEventSource();
    const bare = { ...info, container: { ...info.container, ports: [], mounts: [], networks: [], env: [] } };
    stubFetch({ "GET /containers/web": () => json(200, bare) });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    await view.findByRole("heading", { name: "web" });
    expect(view.getAllByText("none")).toHaveLength(4);
  });

  it("goes to the form and back to the list after deleting", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /containers/web": () => json(200, info),
      "DELETE /containers/aaaaaaaaaaaaaaaa": () => new Response(null, { status: 204 }),
    });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    await view.findByRole("heading", { name: "web" });
    fireEvent.click(view.getByRole("button", { name: "Edit" }));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/containers/aaaaaaaaaaaaaaaa/edit");
    fireEvent.click(view.getByRole("button", { name: "Delete" }));
    const dialog = await view.findByRole("dialog", { name: "Delete container" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers"));
    // the volumes stay unless the box was ticked
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/containers/aaaaaaaaaaaaaaaa",
    ]);
  });

  it("stays where it is after deleting when the page is already gone", async () => {
    stubEventSource();
    const deleted = pending();
    const { calls } = stubFetch({
      "GET /containers/web": () => json(200, info),
      "DELETE /containers/aaaaaaaaaaaaaaaa": () => deleted.response,
    });
    const [shown, setShown] = createSignal(true);
    const view = mount(
      () => (
        <Show when={shown()}>
          <ContainerDetail params={{ id: "web" }} />
        </Show>
      ),
      { path: "/containers/web" },
    );
    await view.findByRole("heading", { name: "web" });
    fireEvent.click(view.getByRole("button", { name: "Delete" }));
    const dialog = await view.findByRole("dialog", { name: "Delete container" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    setShown(false); // the user left the page while the request runs
    deleted.finish();
    await waitFor(() => expect(view.toasts.shown.map((s) => s.message)).toContain("web was deleted."));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/containers/web");
  });

  it("offers Stop and Restart for a restarting or paused container, Start for a stopped one", async () => {
    stubEventSource();
    for (const [status, running, active] of [
      ["restarting", true, true],
      ["paused", true, true],
      ["exited", false, false],
    ] as const) {
      stubFetch({ "GET /containers/web": () => json(200, withState(status, running)) });
      const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
      await view.findByRole("heading", { name: "web" });
      expect(view.queryByRole("button", { name: "Stop" }) !== null).toBe(active);
      expect(view.queryByRole("button", { name: "Restart" }) !== null).toBe(active);
      expect(view.queryByRole("button", { name: "Start" }) !== null).toBe(!active);
      cleanup();
    }
  });

  it("keeps the container busy while its request runs", async () => {
    stubEventSource();
    const stop = pending();
    stubFetch({
      "GET /containers/web": () => json(200, info),
      "POST /containers/aaaaaaaaaaaaaaaa/stop": () => stop.response,
    });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    await view.findByRole("heading", { name: "web" });
    const button = (name: string) => view.getByRole("button", { name }) as HTMLButtonElement;
    fireEvent.click(button("Stop"));
    await waitFor(() => expect(button("Stop").disabled).toBe(true));
    expect(button("Restart").disabled).toBe(true);
    expect(button("Delete").disabled).toBe(true);
    stop.finish();
    await waitFor(() => expect(button("Stop").disabled).toBe(false));
    expect(button("Delete").disabled).toBe(false);
  });

  it("keeps the delete dialog as it is when the container reloads while it is open", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({ "GET /containers/web": () => json(200, info) });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    await view.findByRole("heading", { name: "web" });
    fireEvent.click(view.getByRole("button", { name: "Delete" }));
    const box = await view.findByLabelText("Also delete its anonymous volumes");
    fireEvent.click(box);
    const events = sources.instances.find((s) => s.url === "/ext/docker/api/events");
    events?.emit(
      "docker",
      JSON.stringify({ type: "container", action: "health_status", id: "aaaaaaaaaaaaaaaa" }),
    );
    await settle(600);
    expect(calls.filter((c) => c.path === "/containers/web")).toHaveLength(2);
    expect((view.getByLabelText("Also delete its anonymous volumes") as HTMLInputElement).checked).toBe(true);
  });

  it("has the tabs logs, statistics and console", async () => {
    const sources = stubEventSource();
    stubFetch({ "GET /containers/web": () => json(200, info) });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    await view.findByRole("heading", { name: "web" });
    fireEvent.click(view.getByRole("tab", { name: "Logs" }));
    expect(await view.findByRole("button", { name: "Pause" })).toBeTruthy();
    const logs = sources.instances.find((s) => s.url.includes("/logs"));
    expect(logs?.readyState).not.toBe(2);
    fireEvent.click(view.getByRole("tab", { name: "Statistics" }));
    expect(await view.findByText("Collecting…")).toBeTruthy();
    expect(logs?.readyState).toBe(2); // a tab that is left closes its stream
    fireEvent.click(view.getByRole("tab", { name: "Console" }));
    expect(await view.findByRole("button", { name: "Connect" })).toBeTruthy();
    fireEvent.click(view.getByRole("tab", { name: "Overview" }));
    expect(await view.findByText("127.0.0.1:8080 → 80/tcp")).toBeTruthy();
  });

  it("offers statistics and console only for a running container", async () => {
    const sources = stubEventSource();
    // Docker reports running for a restarting and a paused container as well; the streams need one that runs
    for (const [status, running] of [
      ["restarting", true],
      ["paused", true],
      ["exited", false],
    ] as const) {
      stubFetch({ "GET /containers/web": () => json(200, withState(status, running)) });
      const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
      await view.findByRole("heading", { name: "web" });
      fireEvent.click(view.getByRole("tab", { name: "Statistics" }));
      expect(await view.findByText("The container is not running.")).toBeTruthy();
      fireEvent.click(view.getByRole("tab", { name: "Console" }));
      expect(await view.findByText("The container is not running.")).toBeTruthy();
      expect(view.queryByRole("button", { name: "Connect" })).toBeNull();
      cleanup();
    }
    expect(sources.instances.filter((s) => s.url.includes("/stats"))).toHaveLength(0);
  });

  it("opens new streams of logs and statistics when the container was started again", async () => {
    const sources = stubEventSource();
    let current = info;
    stubFetch({ "GET /containers/web": () => json(200, current) });
    const view = mount(() => <ContainerDetail params={{ id: "web" }} />, { path: "/containers/web" });
    await view.findByRole("heading", { name: "web" });
    const streams = (part: string) => sources.instances.filter((s) => s.url.includes(part));
    const dockerEvent = (action: string) =>
      sources.instances
        .find((s) => s.url === "/ext/docker/api/events")
        ?.emit("docker", JSON.stringify({ type: "container", action, id: "aaaaaaaaaaaaaaaa" }));
    // the container started again at another time; the page never saw it stopped (a quick restart)
    const restart = async (startedAt: string) => {
      current = { ...info, container: { ...info.container, state: { ...info.container.state, startedAt } } };
      dockerEvent("start");
      await settle(600);
    };

    fireEvent.click(view.getByRole("tab", { name: "Logs" }));
    await view.findByRole("button", { name: "Pause" });
    expect(streams("/logs")).toHaveLength(1);
    streams("/logs")[0]?.emit("end", "{}");
    await view.findByText("The container has stopped.");
    dockerEvent("health_status"); // a change that is no start: the stream stays
    await settle(600);
    expect(streams("/logs")).toHaveLength(1);
    await restart("2026-10-10T13:00:00Z");
    expect(streams("/logs")).toHaveLength(2);
    expect(view.queryByText("The container has stopped.")).toBeNull();

    fireEvent.click(view.getByRole("tab", { name: "Statistics" }));
    await view.findByText("Collecting…");
    expect(streams("/stats")).toHaveLength(1);
    streams("/stats")[0]?.emit("stopped", "{}");
    await view.findByText("The container is not running.");
    await restart("2026-10-10T14:00:00Z");
    expect(streams("/stats")).toHaveLength(2);
    expect(await view.findByText("Collecting…")).toBeTruthy();
  });
});
