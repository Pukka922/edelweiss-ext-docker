import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ContainerSummary } from "../api";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { ContainerList } from "./ContainerList";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const web: ContainerSummary = {
  id: "aaaaaaaaaaaaaaaa",
  name: "web",
  image: "nginx:1.29",
  imageId: "sha256:n",
  state: "running",
  status: "Up 2 hours",
  created: 1791633600,
  ports: [
    { hostPort: 8080, containerPort: 80, protocol: "tcp" },
    { containerPort: 443, protocol: "tcp" },
  ],
  networks: ["bridge"],
};
const db: ContainerSummary = {
  ...web,
  id: "bbbbbbbbbbbbbbbb",
  name: "db",
  image: "postgres:17",
  state: "exited",
  status: "Exited (0)",
  ports: [],
};

/** A response the test finishes by hand. */
function pending() {
  let finish!: () => void;
  const response = new Promise<Response>((resolve) => {
    finish = () => resolve(new Response(null, { status: 204 }));
  });
  return { response, finish };
}

describe("ContainerList", () => {
  it("lists the containers with state and published ports", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, [db, web]) });
    const view = mount(ContainerList, { path: "/containers" });
    expect(await view.findByText("web")).toBeTruthy();
    expect(view.getByText("running")).toBeTruthy();
    expect(view.getByText("stopped")).toBeTruthy();
    expect(view.getByText("8080 → 80/tcp")).toBeTruthy();
    expect(view.queryByText(/443/)).toBeNull();
  });

  it("starts and stops containers", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /containers": () => json(200, [db, web]),
      "POST /containers/bbbbbbbbbbbbbbbb/start": () => new Response(null, { status: 204 }),
      "POST /containers/aaaaaaaaaaaaaaaa/stop": () => new Response(null, { status: 204 }),
    });
    const view = mount(ContainerList);
    await view.findByText("db");
    fireEvent.click(view.getByRole("button", { name: "Start db" }));
    fireEvent.click(view.getByRole("button", { name: "Stop web" }));
    await waitFor(() => expect(view.toasts.shown.map((s) => s.message)).toContain("db was started."));
    expect(view.toasts.shown.map((s) => s.message)).toContain("web was stopped.");
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      "/containers/bbbbbbbbbbbbbbbb/start",
      "/containers/aaaaaaaaaaaaaaaa/stop",
    ]);
  });

  it("keeps each container busy until its own request ends", async () => {
    stubEventSource();
    const start = pending();
    const stop = pending();
    stubFetch({
      "GET /containers": () => json(200, [db, web]),
      "POST /containers/bbbbbbbbbbbbbbbb/start": () => start.response,
      "POST /containers/aaaaaaaaaaaaaaaa/stop": () => stop.response,
    });
    const view = mount(ContainerList);
    await view.findByText("db");
    const button = (name: string) => view.getByRole("button", { name }) as HTMLButtonElement;
    fireEvent.click(button("Start db"));
    fireEvent.click(button("Stop web"));
    await waitFor(() => expect(button("Start db").disabled).toBe(true));
    expect(button("Stop web").disabled).toBe(true);
    start.finish();
    await waitFor(() => expect(button("Start db").disabled).toBe(false));
    expect(button("Stop web").disabled).toBe(true); // its own request still runs
    expect(button("Delete web").disabled).toBe(true);
    expect(button("Delete db").disabled).toBe(false);
    stop.finish();
    await waitFor(() => expect(button("Stop web").disabled).toBe(false));
    expect(button("Delete web").disabled).toBe(false);
  });

  it("offers Stop and Restart for a restarting or paused container, Start for a stopped one", async () => {
    stubEventSource();
    const crash: ContainerSummary = {
      ...web,
      id: "cccccccccccccccc",
      name: "crash",
      state: "restarting",
      status: "Restarting (1) 5 seconds ago",
    };
    const frozen: ContainerSummary = {
      ...web,
      id: "dddddddddddddddd",
      name: "frozen",
      state: "paused",
      status: "Up 2 hours (Paused)",
    };
    stubFetch({ "GET /containers": () => json(200, [crash, frozen, db]) });
    const view = mount(ContainerList);
    await view.findByText("crash");
    for (const name of ["crash", "frozen"]) {
      expect(view.getByRole("button", { name: `Stop ${name}` })).toBeTruthy();
      expect(view.getByRole("button", { name: `Restart ${name}` })).toBeTruthy();
      expect(view.queryByRole("button", { name: `Start ${name}` })).toBeNull();
    }
    expect(view.getByRole("button", { name: "Start db" })).toBeTruthy();
    expect(view.queryByRole("button", { name: "Stop db" })).toBeNull();
    expect(view.queryByRole("button", { name: "Restart db" })).toBeNull();
  });

  it("shows a failed action as an error toast with the translated text", async () => {
    stubEventSource();
    stubFetch({
      "GET /containers": () => json(200, [db]),
      "POST /containers/bbbbbbbbbbbbbbbb/start": () =>
        json(409, { error: { code: "conflict", message: "port is already allocated" } }),
    });
    const view = mount(ContainerList);
    await view.findByText("db");
    fireEvent.click(view.getByRole("button", { name: "Start db" }));
    await waitFor(() => expect(view.toasts.shown).toHaveLength(1));
    expect(view.toasts.shown[0]).toEqual({
      kind: "error",
      message: "Docker refused: port is already allocated",
    });
  });

  it("keeps the anonymous volumes of a deleted container unless asked to delete them", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /containers": () => json(200, [web]),
      "DELETE /containers/aaaaaaaaaaaaaaaa": () => new Response(null, { status: 204 }),
    });
    const view = mount(ContainerList);
    await view.findByText("web");
    fireEvent.click(view.getByRole("button", { name: "Delete web" }));
    const dialog = await view.findByRole("dialog", { name: "Delete container" });
    expect((view.getByLabelText("Also delete its anonymous volumes") as HTMLInputElement).checked).toBe(
      false,
    );
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/containers/aaaaaaaaaaaaaaaa",
    ]);
  });

  it("deletes a container with its anonymous volumes when asked to", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /containers": () => json(200, [web]),
      "DELETE /containers/aaaaaaaaaaaaaaaa": () => new Response(null, { status: 204 }),
    });
    const view = mount(ContainerList);
    await view.findByText("web");
    fireEvent.click(view.getByRole("button", { name: "Delete web" }));
    const dialog = await view.findByRole("dialog", { name: "Delete container" });
    fireEvent.click(view.getByLabelText("Also delete its anonymous volumes"));
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    expect(calls.find((c) => c.method === "DELETE")?.path).toBe("/containers/aaaaaaaaaaaaaaaa?volumes=1");
  });

  it("reloads when Docker reports a change of a container", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({ "GET /containers": () => json(200, [web]) });
    const view = mount(ContainerList);
    await view.findByText("web");
    const events = sources.instances.find((s) => s.url === "/ext/docker/api/events");
    // the page lists containers only: the events of other types do not reload it
    events?.emit("docker", JSON.stringify({ type: "volume", action: "create", id: "x" }));
    await settle(600);
    expect(calls.filter((c) => c.path === "/containers")).toHaveLength(1);
    // several events of a container within the debounce time reload it once
    events?.emit("docker", JSON.stringify({ type: "container", action: "start", id: "aaaaaaaaaaaaaaaa" }));
    events?.emit("docker", JSON.stringify({ type: "container", action: "die", id: "aaaaaaaaaaaaaaaa" }));
    await settle(600);
    expect(calls.filter((c) => c.path === "/containers")).toHaveLength(2);
  });

  it("shows an error of the backend", async () => {
    stubEventSource();
    stubFetch({
      "GET /containers": () => json(503, { error: { code: "docker_unavailable", message: "gone" } }),
    });
    const view = mount(ContainerList);
    expect(await view.findByText("Docker is not running: gone")).toBeTruthy();
  });
});
