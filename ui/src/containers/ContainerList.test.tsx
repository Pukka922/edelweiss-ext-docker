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

/** The list with one running container, web. */
async function mountListWithWeb() {
  stubEventSource();
  stubFetch({ "GET /containers": () => json(200, [web]) });
  const view = mount(ContainerList, { path: "/containers" });
  await view.findByText("web");
  return view;
}

describe("ContainerList", () => {
  it("has no Created column and shows the actions as icons with their names", async () => {
    const view = await mountListWithWeb();
    expect(view.queryByRole("columnheader", { name: "Created" })).toBeNull();
    // the cell of the column is gone from the rows too: every row has a cell for each header
    const headers = view.getAllByRole("columnheader");
    expect(headers.map((h) => h.textContent)).toEqual(["Name", "Image", "State", "Ports", "Actions"]);
    expect(view.getByText("web").closest("tr")?.querySelectorAll("td")).toHaveLength(headers.length);
    const stop = view.getByRole("button", { name: "Stop web" });
    expect(stop.textContent?.trim()).toBe("");
    expect(stop.querySelector("svg")).not.toBeNull();
    expect(stop.closest("[data-tip]")?.getAttribute("data-tip")).toBe("Stop");
    for (const [name, tip] of [
      ["Restart web", "Restart"],
      ["Edit web", "Edit"],
      ["Delete web", "Delete"],
    ]) {
      const button = view.getByRole("button", { name });
      expect(button.querySelector("svg")).not.toBeNull();
      expect(button.closest("[data-tip]")?.getAttribute("data-tip")).toBe(tip);
    }
  });

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

/** The names of the rows of the table, in order. */
const names = (view: ReturnType<typeof mount>) =>
  view
    .getAllByRole("row")
    .slice(1)
    .map((r) => r.querySelector("td")?.textContent?.trim());

/** n containers c01, c02 …, in the order Docker could answer with: reversed. */
const many = (n: number): ContainerSummary[] =>
  Array.from({ length: n }, (_, i) => ({
    ...web,
    id: `id${String(i + 1).padStart(2, "0")}`,
    name: `c${String(i + 1).padStart(2, "0")}`,
    ports: [],
  })).reverse();

describe("ContainerList sorting, search and paging", () => {
  it("shows the containers sorted by name", async () => {
    stubEventSource();
    const b = { ...web, id: "b", name: "Beta10" };
    const c = { ...web, id: "c", name: "beta2" };
    const a = { ...web, id: "a", name: "alpha" };
    stubFetch({ "GET /containers": () => json(200, [b, c, a]) });
    const view = mount(ContainerList);
    await view.findByText("alpha");
    expect(names(view)).toEqual(["alpha", "beta2", "Beta10"]);
  });

  it("searches name, image, state and published host port", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, [db, web]) });
    const view = mount(ContainerList);
    await view.findByText("web");
    const search = view.getByRole("searchbox", { name: "Search containers …" }) as HTMLInputElement;
    expect(search.placeholder).toBe("Search containers …");
    fireEvent.input(search, { target: { value: "POSTGRES" } });
    expect(names(view)).toEqual(["db"]);
    fireEvent.input(search, { target: { value: "nginx" } });
    expect(names(view)).toEqual(["web"]);
    fireEvent.input(search, { target: { value: "stopped" } });
    expect(names(view)).toEqual(["db"]);
    fireEvent.input(search, { target: { value: "8080" } });
    expect(names(view)).toEqual(["web"]);
    fireEvent.input(search, { target: { value: "443" } }); // not published: no host port
    expect(view.queryByRole("table")).toBeNull();
  });

  it("says so when nothing matches, and hides the table and the pager", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, many(30)) });
    const view = mount(ContainerList);
    await view.findByText("c01");
    fireEvent.input(view.getByRole("searchbox"), { target: { value: "zzz" } });
    expect(view.getByText("No matches for “zzz”")).toBeTruthy();
    expect(view.queryByRole("table")).toBeNull();
    expect(view.queryByText(/Page \d of/)).toBeNull();
    expect(view.queryByText("There are no containers yet.")).toBeNull();
  });

  it("pages 30 containers into two pages of 25 and 5", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, many(30)) });
    const view = mount(ContainerList);
    await view.findByText("c01");
    expect(names(view)).toHaveLength(25);
    expect(names(view)[0]).toBe("c01");
    expect(view.getByText("Page 1 of 2")).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Next page" }));
    expect(view.getByText("Page 2 of 2")).toBeTruthy();
    expect(names(view)).toEqual(["c26", "c27", "c28", "c29", "c30"]);
    fireEvent.click(view.getByRole("button", { name: "First page" }));
    expect(names(view)[0]).toBe("c01");
  });

  it("shows no pager for a single page", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, many(25)) });
    const view = mount(ContainerList);
    await view.findByText("c01");
    expect(view.queryByRole("button", { name: "Next page" })).toBeNull();
  });

  it("goes back to page 1 when the search changes", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, many(60)) });
    const view = mount(ContainerList);
    await view.findByText("c01");
    fireEvent.click(view.getByRole("button", { name: "Last page" }));
    expect(view.getByText("Page 3 of 3")).toBeTruthy();
    fireEvent.input(view.getByRole("searchbox"), { target: { value: "c" } });
    expect(view.getByText("Page 1 of 3")).toBeTruthy();
    expect(names(view)[0]).toBe("c01");
  });

  it("clamps the page when a reload leaves fewer containers", async () => {
    const sources = stubEventSource();
    let list = many(30);
    stubFetch({ "GET /containers": () => json(200, list) });
    const view = mount(ContainerList);
    await view.findByText("c01");
    fireEvent.click(view.getByRole("button", { name: "Next page" }));
    expect(view.getByText("Page 2 of 2")).toBeTruthy();
    list = many(10);
    sources.instances
      .find((s) => s.url === "/ext/docker/api/events")
      ?.emit("docker", JSON.stringify({ type: "container", action: "destroy", id: "x" }));
    await waitFor(() => expect(names(view)).toHaveLength(10));
    expect(view.queryByText(/Page \d of/)).toBeNull();
    expect(names(view)[0]).toBe("c01");
  });

  it("keeps search and page across an unmount and a new mount", async () => {
    stubEventSource();
    stubFetch({ "GET /containers": () => json(200, many(60)) });
    const first = mount(ContainerList);
    await first.findByText("c01");
    fireEvent.input(first.getByRole("searchbox"), { target: { value: "c" } });
    fireEvent.click(first.getByRole("button", { name: "Next page" }));
    cleanup();
    const view = mount(ContainerList);
    await view.findByText("c26");
    expect((view.getByRole("searchbox") as HTMLInputElement).value).toBe("c");
    expect(view.getByText("Page 2 of 3")).toBeTruthy();
  });
});
