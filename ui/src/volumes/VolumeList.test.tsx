import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Volume } from "../api";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { VolumeList } from "./VolumeList";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const data: Volume = {
  name: "web-data",
  driver: "local",
  mountpoint: "/var/lib/docker/volumes/web-data/_data",
  createdAt: "2026-10-10T12:00:00Z",
  scope: "local",
  options: {},
  anonymous: false,
  usedBy: [{ id: "c1", name: "web" }],
};
const anon: Volume = {
  ...data,
  name: "f".repeat(64),
  mountpoint: "/var/lib/docker/volumes/fff/_data",
  anonymous: true,
  usedBy: [],
};

const messages = (view: ReturnType<typeof mount>) => view.toasts.shown.map((s) => s.message);
const type = (el: HTMLElement, value: string) => fireEvent.input(el, { target: { value } });

describe("VolumeList", () => {
  it("lists the volumes with their users", async () => {
    stubEventSource();
    stubFetch({ "GET /volumes": () => json(200, [data, anon]) });
    const view = mount(VolumeList, { path: "/volumes" });
    expect(await view.findByText("web-data")).toBeTruthy();
    expect(view.getByText("ffffffffffff")).toBeTruthy();
    expect(view.getByText("anonymous")).toBeTruthy();
    expect(view.getByText("unused")).toBeTruthy();
    expect((view.getByRole("button", { name: "Delete web-data" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(view.getByRole("button", { name: "web" }));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/containers/c1");
  });

  it("creates an NFS volume with driver options", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /volumes": () => json(200, []),
      "POST /volumes": () => json(201, { ...data, name: "nas", usedBy: [] }),
    });
    const view = mount(VolumeList, { path: "/volumes" });
    fireEvent.click(await view.findByRole("button", { name: "Create volume" }));
    await view.findByRole("dialog", { name: "Create volume" });
    type(view.getByLabelText("Name"), "nas");
    for (let i = 0; i < 3; i++) fireEvent.click(view.getByRole("button", { name: "Add option" }));
    const keys = view.getAllByLabelText("Key");
    const values = view.getAllByLabelText("Value");
    const options = [
      ["type", "nfs"],
      ["o", "addr=10.0.0.5,rw"],
      ["device", ":/export"],
    ] as const;
    for (const [i, [k, v]] of options.entries()) {
      type(keys[i]!, k);
      type(values[i]!, v);
    }
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(messages(view)).toContain("The volume nas was created."));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      name: "nas",
      driver: "local",
      options: { type: "nfs", o: "addr=10.0.0.5,rw", device: ":/export" },
    });
    await waitFor(() => expect(view.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(calls.filter((c) => c.method === "GET")).toHaveLength(2)); // the list is loaded again
  });

  it("lets Docker pick the name, and shows the picked name short", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /volumes": () => json(200, []),
      "POST /volumes": () => json(201, anon),
    });
    const view = mount(VolumeList, { path: "/volumes" });
    fireEvent.click(await view.findByRole("button", { name: "Create volume" }));
    await view.findByRole("dialog", { name: "Create volume" });
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(messages(view)).toContain("The volume ffffffffffff was created."));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name: "", driver: "local", options: {} });
  });

  it("focuses the name field; Escape closes the dialog and gives the focus back", async () => {
    stubEventSource();
    const { calls } = stubFetch({ "GET /volumes": () => json(200, []) });
    const view = mount(VolumeList, { path: "/volumes" });
    const opener = await view.findByRole("button", { name: "Create volume" });
    opener.focus();
    fireEvent.click(opener);
    await view.findByRole("dialog", { name: "Create volume" });
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
      "GET /volumes": () => json(200, []),
      "POST /volumes": () =>
        json(400, { error: { code: "invalid_request", message: "name may only contain …", field: "name" } }),
    });
    const view = mount(VolumeList, { path: "/volumes" });
    fireEvent.click(await view.findByRole("button", { name: "Create volume" }));
    await view.findByRole("dialog", { name: "Create volume" });
    type(view.getByLabelText("Name"), "a b");
    fireEvent.click(view.getByRole("button", { name: "Create" }));
    expect(await view.findByText("Invalid input: name may only contain …")).toBeTruthy();
    const name = view.getByLabelText("Name");
    expect(name.getAttribute("aria-invalid")).toBe("true");
    expect(name.classList.contains("input-error")).toBe(true);
    expect(view.getByLabelText("Driver").getAttribute("aria-invalid")).not.toBe("true");
    expect(view.getByRole("dialog", { name: "Create volume" })).toBeTruthy(); // the dialog stays for a new try
  });

  it("deletes an unused volume", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /volumes": () => json(200, [data, anon]),
      [`DELETE /volumes/${anon.name}`]: () => new Response(null, { status: 204 }),
    });
    const view = mount(VolumeList, { path: "/volumes" });
    await view.findByText("web-data");
    fireEvent.click(view.getByRole("button", { name: "Delete ffffffffffff" }));
    const dialog = await view.findByRole("dialog", { name: "Delete volume" });
    expect(view.getByText("Delete the volume ffffffffffff and its data?")).toBeTruthy();
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(messages(view)).toContain("The volume ffffffffffff was deleted."));
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
    await waitFor(() => expect(calls.filter((c) => c.path === "/volumes")).toHaveLength(2));
  });

  it("shows the refusal of a volume that a container began to use, and loads the list again", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /volumes": () => json(200, [anon]),
      [`DELETE /volumes/${anon.name}`]: () =>
        json(409, {
          error: {
            code: "volume_in_use",
            message: "the volume is used by containers",
            containers: [{ id: "c1", name: "web" }],
          },
        }),
    });
    const view = mount(VolumeList, { path: "/volumes" });
    fireEvent.click(await view.findByRole("button", { name: "Delete ffffffffffff" }));
    const dialog = await view.findByRole("dialog", { name: "Delete volume" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() =>
      expect(view.toasts.shown).toContainEqual({
        kind: "error",
        message: "The volume is used by containers.",
      }),
    );
    await waitFor(() => expect(calls.filter((c) => c.path === "/volumes")).toHaveLength(2));
  });

  it("reloads when Docker reports a change of a volume or a container", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({ "GET /volumes": () => json(200, [data]) });
    const view = mount(VolumeList, { path: "/volumes" });
    await view.findByText("web-data");
    const events = sources.instances.find((s) => s.url === "/ext/docker/api/events");
    const reads = () => calls.filter((c) => c.path === "/volumes").length;
    // the page lists volumes and their users: an event of another type does not reload it
    events?.emit("docker", JSON.stringify({ type: "image", action: "pull", id: "x" }));
    await settle(600);
    expect(reads()).toBe(1);
    events?.emit("docker", JSON.stringify({ type: "volume", action: "create", id: "v1" }));
    await settle(600);
    expect(reads()).toBe(2);
    events?.emit("docker", JSON.stringify({ type: "container", action: "start", id: "c1" }));
    await settle(600);
    expect(reads()).toBe(3);
  });
});
