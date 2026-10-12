import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { createSignal, Show } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ContainerSummary, NetworkDetail as Detail, NetworkInfo } from "../api";
import { dockerErrors } from "../gate";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { NetworkDetail } from "./NetworkDetail";
import { NetworkList } from "./NetworkList";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const bridge: NetworkInfo = {
  id: "b",
  name: "bridge",
  driver: "bridge",
  scope: "local",
  subnet: "172.17.0.0/16",
  gateway: "172.17.0.1",
  internal: false,
  predefined: true,
  containers: 0,
};
const front: NetworkInfo = {
  id: "f",
  name: "front",
  driver: "bridge",
  scope: "local",
  subnet: "172.30.0.0/24",
  gateway: "172.30.0.1",
  internal: true,
  predefined: false,
  containers: 1,
};
const detail: Detail = {
  ...front,
  options: { "com.docker.network.bridge.name": "br-front" },
  labels: {},
  attached: [{ id: "c1", name: "web", ipv4: "172.30.0.2" }],
};
const container = (id: string, name: string): ContainerSummary => ({
  id,
  name,
  image: "busybox",
  imageId: "sha256:b",
  state: "running",
  status: "Up",
  created: 0,
  ports: [],
  networks: [],
});

const messages = (view: ReturnType<typeof mount>) => view.toasts.shown.map((s) => s.message);
const type = (el: HTMLElement, value: string) => fireEvent.input(el, { target: { value } });
const refused = (code: string, message: string, status = 409, field?: string) =>
  json(status, { error: { code, message, ...(field ? { field } : {}) } });

describe("NetworkList", () => {
  it("lists the networks; the built-in ones cannot be deleted", async () => {
    stubEventSource();
    stubFetch({ "GET /networks": () => json(200, [bridge, front]) });
    const view = mount(NetworkList, { path: "/networks" });
    expect(await view.findByText("172.30.0.0/24")).toBeTruthy();
    expect(view.getByText("built in")).toBeTruthy();
    expect(view.getByText("internal")).toBeTruthy();
    expect((view.getByRole("button", { name: "Delete bridge" }) as HTMLButtonElement).disabled).toBe(true);
    // the tooltip of a disabled button says why, the one of an enabled button names the action
    const tip = (name: string) =>
      view.getByRole("button", { name }).closest("[data-tip]")?.getAttribute("data-tip");
    expect(tip("Delete bridge")).toBe("The networks bridge, host and none cannot be deleted.");
    expect(tip("Delete front")).toBe("Delete");
    fireEvent.click(view.getByRole("button", { name: "front" }));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/networks/f");
  });

  it("creates a macvlan network with a parent interface", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /networks": () => json(200, [bridge]),
      "POST /networks": () => json(201, { id: "n1" }),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Create network" }));
    await view.findByRole("dialog", { name: "Create network" });
    type(view.getByLabelText("Name"), "lan");
    fireEvent.change(view.getByLabelText("Driver"), { target: { value: "macvlan" } });
    type(view.getByLabelText("Subnet"), "192.168.1.0/24");
    type(view.getByLabelText("Gateway"), "192.168.1.1");
    fireEvent.click(view.getByRole("button", { name: "Add option" }));
    type(view.getByLabelText("Key"), "parent");
    type(view.getByLabelText("Value"), "eth0");
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(messages(view)).toContain("The network lan was created."));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "lan",
      driver: "macvlan",
      subnet: "192.168.1.0/24",
      gateway: "192.168.1.1",
      internal: false,
      options: { parent: "eth0" },
    });
    await waitFor(() => expect(view.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(calls.filter((c) => c.method === "GET")).toHaveLength(2)); // the list is loaded again
  });

  it("creates an internal bridge network with the defaults", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /networks": () => json(200, []),
      "POST /networks": () => json(201, { id: "n2" }),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Create network" }));
    await view.findByRole("dialog", { name: "Create network" });
    expect((view.getByLabelText("Driver") as HTMLSelectElement).value).toBe("bridge");
    type(view.getByLabelText("Name"), " back ");
    fireEvent.click(view.getByLabelText("Internal (no access to the outside)"));
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(messages(view)).toContain("The network back was created."));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "back",
      driver: "bridge",
      subnet: "",
      gateway: "",
      internal: true,
      options: {},
    });
  });

  it("focuses the name field; Escape closes the dialog and gives the focus back", async () => {
    stubEventSource();
    const { calls } = stubFetch({ "GET /networks": () => json(200, []) });
    const view = mount(NetworkList, { path: "/networks" });
    const opener = await view.findByRole("button", { name: "Create network" });
    opener.focus();
    fireEvent.click(opener);
    await view.findByRole("dialog", { name: "Create network" });
    expect(document.activeElement).toBe(view.getByLabelText("Name"));
    fireEvent.keyDown(document, { key: "Escape" });
    await settle();
    expect(view.queryByRole("dialog")).toBeNull();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(document.activeElement).toBe(opener);
  });

  it("marks the field the backend refused", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks": () => json(200, []),
      "POST /networks": () =>
        refused("invalid_request", "subnet must be a network like 172.30.0.0/24", 400, "subnet"),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Create network" }));
    await view.findByRole("dialog", { name: "Create network" });
    type(view.getByLabelText("Name"), "lan");
    type(view.getByLabelText("Subnet"), "172.30.0.5/24");
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    expect(await view.findByText("Invalid input: subnet must be a network like 172.30.0.0/24")).toBeTruthy();
    const subnet = view.getByLabelText("Subnet");
    expect(subnet.getAttribute("aria-invalid")).toBe("true");
    expect(subnet.classList.contains("input-error")).toBe(true);
    expect(view.getByLabelText("Gateway").getAttribute("aria-invalid")).not.toBe("true");
    expect(view.getByLabelText("Name").getAttribute("aria-invalid")).not.toBe("true");
  });

  it("marks the driver select the backend refused", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks": () => json(200, []),
      "POST /networks": () =>
        refused("invalid_request", "driver must be bridge, macvlan or ipvlan", 400, "driver"),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Create network" }));
    await view.findByRole("dialog", { name: "Create network" });
    type(view.getByLabelText("Name"), "lan");
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    await view.findByText("Invalid input: driver must be bridge, macvlan or ipvlan");
    const driver = view.getByLabelText("Driver");
    expect(driver.getAttribute("aria-invalid")).toBe("true");
    expect(driver.classList.contains("select-error")).toBe(true);
  });

  it("deletes a network", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /networks": () => json(200, [front]),
      "DELETE /networks/f": () => new Response(null, { status: 204 }),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Delete front" }));
    const dialog = await view.findByRole("dialog", { name: "Delete network" });
    expect(view.getByText("Delete the network front?")).toBeTruthy();
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(messages(view)).toContain("The network front was deleted."));
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/networks/f")).toBe(true);
    await waitFor(() => expect(calls.filter((c) => c.path === "/networks")).toHaveLength(2));
  });

  it("shows the refusal of a network that still has containers, and loads the list again", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /networks": () => json(200, [front]),
      "DELETE /networks/f": () => refused("conflict", "the network still has connected containers"),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Delete front" }));
    const dialog = await view.findByRole("dialog", { name: "Delete network" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() =>
      expect(view.toasts.shown).toContainEqual({
        kind: "error",
        message: "Docker refused: the network still has connected containers",
      }),
    );
    await waitFor(() => expect(calls.filter((c) => c.path === "/networks")).toHaveLength(2));
  });

  it("shows the refusal of a built-in network in the words of its code", async () => {
    stubEventSource();
    // the button of a built-in network is disabled; if the list did not flag it, the backend's answer is shown anyway
    stubFetch({
      "GET /networks": () => json(200, [{ ...bridge, predefined: false }]),
      "DELETE /networks/b": () =>
        refused("network_predefined", "the networks bridge, host and none cannot be deleted"),
    });
    const view = mount(NetworkList, { path: "/networks" });
    fireEvent.click(await view.findByRole("button", { name: "Delete bridge" }));
    const dialog = await view.findByRole("dialog", { name: "Delete network" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() =>
      expect(view.toasts.shown).toContainEqual({
        kind: "error",
        message: "The networks bridge, host and none cannot be deleted.",
      }),
    );
  });

  it("reloads when Docker reports a change of a network or a container", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({ "GET /networks": () => json(200, [front]) });
    const view = mount(NetworkList, { path: "/networks" });
    await view.findByText("172.30.0.0/24");
    const events = sources.instances.find((s) => s.url === "/ext/docker/api/events");
    const reads = () => calls.filter((c) => c.path === "/networks").length;
    // the page lists networks and their containers: an event of another type does not reload it
    events?.emit("docker", JSON.stringify({ type: "image", action: "pull", id: "x" }));
    await settle(600);
    expect(reads()).toBe(1);
    events?.emit("docker", JSON.stringify({ type: "network", action: "create", id: "n1" }));
    await settle(600);
    expect(reads()).toBe(2);
    events?.emit("docker", JSON.stringify({ type: "container", action: "start", id: "c1" }));
    await settle(600);
    expect(reads()).toBe(3);
  });
});

describe("NetworkDetail", () => {
  it("connects and disconnects containers", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, [container("c1", "web"), container("c2", "db")]),
      "POST /networks/f/connect": () => new Response(null, { status: 204 }),
      "POST /networks/f/disconnect": () => new Response(null, { status: 204 }),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    expect(await view.findByRole("heading", { name: "front" })).toBeTruthy();
    expect(view.getByText("172.30.0.2")).toBeTruthy();
    expect(view.getByText("com.docker.network.bridge.name=br-front")).toBeTruthy();
    await view.findByRole("option", { name: "db" });
    expect(view.queryByRole("option", { name: "web" })).toBeNull(); // connected already
    fireEvent.change(view.getByLabelText("Container"), { target: { value: "c2" } });
    type(view.getByLabelText("IPv4 address"), "172.30.0.9");
    fireEvent.click(view.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(messages(view)).toContain("db was connected."));
    expect(calls.find((c) => c.path === "/networks/f/connect")?.body).toEqual({
      container: "c2",
      ipv4: "172.30.0.9",
    });
    // the form is empty again and the network is read again
    await waitFor(() => expect((view.getByLabelText("IPv4 address") as HTMLInputElement).value).toBe(""));
    expect((view.getByLabelText("Container") as HTMLSelectElement).value).toBe("");
    await waitFor(() => expect(calls.filter((c) => c.path === "/networks/f")).toHaveLength(2));
    const disconnect = view.getByRole("button", { name: "Disconnect web" });
    expect(disconnect.textContent?.trim()).toBe("");
    expect(disconnect.querySelector("svg")).not.toBeNull();
    expect(disconnect.closest("[data-tip]")?.getAttribute("data-tip")).toBe("Disconnect");
    fireEvent.click(disconnect);
    await waitFor(() => expect(messages(view)).toContain("web was disconnected."));
    expect(calls.find((c) => c.path === "/networks/f/disconnect")?.body).toEqual({ container: "c1" });
  });

  it("offers Connect only for a chosen container", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, [container("c2", "db")]),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("option", { name: "db" });
    const connect = view.getByRole("button", { name: "Connect" }) as HTMLButtonElement;
    expect(connect.disabled).toBe(true);
    fireEvent.change(view.getByLabelText("Container"), { target: { value: "c2" } });
    expect(connect.disabled).toBe(false);
  });

  it("shows a refused connect and keeps the form as it is", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, [container("c2", "db")]),
      "POST /networks/f/connect": () =>
        refused("invalid_request", "ipv4 must be an IPv4 address", 400, "ipv4"),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("option", { name: "db" });
    fireEvent.change(view.getByLabelText("Container"), { target: { value: "c2" } });
    type(view.getByLabelText("IPv4 address"), "nonsense");
    fireEvent.click(view.getByRole("button", { name: "Connect" }));
    await waitFor(() =>
      expect(view.toasts.shown).toContainEqual({
        kind: "error",
        message: "Invalid input: ipv4 must be an IPv4 address",
      }),
    );
    await waitFor(() =>
      expect((view.getByRole("button", { name: "Connect" }) as HTMLButtonElement).disabled).toBe(false),
    );
    expect((view.getByLabelText("Container") as HTMLSelectElement).value).toBe("c2");
    expect((view.getByLabelText("IPv4 address") as HTMLInputElement).value).toBe("nonsense");
    // the field the backend named is marked, the other one is not
    const address = view.getByLabelText("IPv4 address");
    expect(address.getAttribute("aria-invalid")).toBe("true");
    expect(address.classList.contains("input-error")).toBe(true);
    expect(view.getByLabelText("Container").getAttribute("aria-invalid")).not.toBe("true");
    // correcting the address takes the mark away
    type(address, "172.30.0.9");
    expect(address.getAttribute("aria-invalid")).not.toBe("true");
    expect(address.classList.contains("input-error")).toBe(false);
  });

  it("marks the container select when the backend refuses the container", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, [container("c2", "db")]),
      "POST /networks/f/connect": () => refused("invalid_request", "container is required", 400, "container"),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("option", { name: "db" });
    const select = view.getByLabelText("Container");
    fireEvent.change(select, { target: { value: "c2" } });
    fireEvent.click(view.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(select.getAttribute("aria-invalid")).toBe("true"));
    expect(select.classList.contains("select-error")).toBe(true);
    expect(view.getByLabelText("IPv4 address").getAttribute("aria-invalid")).not.toBe("true");
    // choosing again takes the mark away
    fireEvent.change(select, { target: { value: "c2" } });
    expect(select.getAttribute("aria-invalid")).not.toBe("true");
  });

  it("shows a failed container list in the connect form, tells the gate and tries again on request", async () => {
    stubEventSource();
    let failing = true;
    stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () =>
        failing ? refused("docker_unavailable", "gone", 503) : json(200, [container("c2", "db")]),
    });
    const before = dockerErrors();
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    expect(await view.findByText("Docker is not running: gone")).toBeTruthy();
    expect(dockerErrors()).toBeGreaterThan(before); // the App can replace the page by its hint
    // the rest of the page is there; nothing can be connected
    expect(view.getByRole("heading", { name: "front" })).toBeTruthy();
    expect(view.queryByRole("option", { name: "db" })).toBeNull();
    expect((view.getByRole("button", { name: "Connect" }) as HTMLButtonElement).disabled).toBe(true);
    failing = false;
    fireEvent.click(view.getByRole("button", { name: "Retry" }));
    expect(await view.findByRole("option", { name: "db" })).toBeTruthy();
    expect(view.queryByText("Docker is not running: gone")).toBeNull();
  });

  it("does not offer a container that is no longer listed, and Connect does nothing without one", async () => {
    const sources = stubEventSource();
    let listed = [container("c1", "web"), container("c2", "db")];
    const { calls } = stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, listed),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("option", { name: "db" });
    const select = view.getByLabelText("Container") as HTMLSelectElement;
    const connect = view.getByRole("button", { name: "Connect" }) as HTMLButtonElement;
    fireEvent.change(select, { target: { value: "c2" } });
    expect(connect.disabled).toBe(false);
    // the chosen container is removed while the page is open
    listed = [container("c1", "web")];
    sources.instances
      .find((s) => s.url === "/ext/docker/api/events")
      ?.emit("docker", JSON.stringify({ type: "container", action: "destroy", id: "c2" }));
    await waitFor(() => expect(view.queryByRole("option", { name: "db" })).toBeNull());
    expect(select.value).toBe("");
    expect(connect.disabled).toBe(true);
    // not even a submit with Enter sends anything
    fireEvent.submit(select.closest("form")!);
    await settle();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("goes back to the list after deleting", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, []),
      "DELETE /networks/f": () => new Response(null, { status: 204 }),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("heading", { name: "front" });
    fireEvent.click(view.getByRole("button", { name: "Delete" }));
    const dialog = await view.findByRole("dialog", { name: "Delete network" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/networks"));
    expect(messages(view)).toContain("The network front was deleted.");
  });

  it("stays on the page when the network cannot be deleted, and says why", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, []),
      "DELETE /networks/f": () => refused("conflict", "the network still has connected containers"),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("heading", { name: "front" });
    fireEvent.click(view.getByRole("button", { name: "Delete" }));
    const dialog = await view.findByRole("dialog", { name: "Delete network" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() =>
      expect(view.toasts.shown).toContainEqual({
        kind: "error",
        message: "Docker refused: the network still has connected containers",
      }),
    );
    await settle();
    expect(view.history.get()).toBe("/x/docker/networks/f");
    expect(view.getByRole("heading", { name: "front" })).toBeTruthy();
  });

  it("stays where it is after deleting when the page is already gone", async () => {
    stubEventSource();
    let release!: () => void;
    const deleted = new Promise<Response>((resolve) => {
      release = () => resolve(new Response(null, { status: 204 }));
    });
    const { calls } = stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, []),
      "DELETE /networks/f": () => deleted,
    });
    const [shown, setShown] = createSignal(true);
    const view = mount(
      () => (
        <Show when={shown()}>
          <NetworkDetail params={{ id: "f" }} />
        </Show>
      ),
      { path: "/networks/f" },
    );
    await view.findByRole("heading", { name: "front" });
    fireEvent.click(view.getByRole("button", { name: "Delete" }));
    const dialog = await view.findByRole("dialog", { name: "Delete network" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
    setShown(false); // the user left the page while the request runs
    release();
    await waitFor(() => expect(messages(view)).toContain("The network front was deleted."));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/networks/f");
  });

  it("does not offer to delete a built-in network", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/b": () => json(200, { ...bridge, options: {}, labels: {}, attached: [] }),
      "GET /containers": () => json(200, []),
    });
    const view = mount(() => <NetworkDetail params={{ id: "b" }} />, { path: "/networks/b" });
    await view.findByRole("heading", { name: "bridge" });
    expect(view.getByText("built in")).toBeTruthy();
    expect(view.queryByRole("button", { name: "Delete" })).toBeNull();
    expect(view.getByText("No container is connected.")).toBeTruthy();
  });

  it("shows a network that cannot be read", async () => {
    stubEventSource();
    stubFetch({
      "GET /networks/zz": () => refused("not_found", "network zz not found", 404),
      "GET /containers": () => json(200, []),
    });
    const view = mount(() => <NetworkDetail params={{ id: "zz" }} />, { path: "/networks/zz" });
    expect(await view.findByText("Not found: network zz not found")).toBeTruthy();
  });

  it("reloads the network and the containers when Docker reports a change of a network or a container", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, [container("c2", "db")]),
    });
    const view = mount(() => <NetworkDetail params={{ id: "f" }} />, { path: "/networks/f" });
    await view.findByRole("option", { name: "db" });
    const events = sources.instances.find((s) => s.url === "/ext/docker/api/events");
    const reads = (path: string) => calls.filter((c) => c.path === path).length;
    // an event of another type does not reload the page
    events?.emit("docker", JSON.stringify({ type: "volume", action: "create", id: "x" }));
    await settle(600);
    expect([reads("/networks/f"), reads("/containers")]).toEqual([1, 1]);
    events?.emit("docker", JSON.stringify({ type: "network", action: "connect", id: "f" }));
    await settle(600);
    expect([reads("/networks/f"), reads("/containers")]).toEqual([2, 2]);
    events?.emit("docker", JSON.stringify({ type: "container", action: "start", id: "c2" }));
    await settle(600);
    expect([reads("/networks/f"), reads("/containers")]).toEqual([3, 3]);
  });
});
