import { cleanup, fireEvent, waitFor, within } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Spec } from "../api";
import { info } from "../test/fixtures";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { ContainerCreate, ContainerEdit, needsRecreate } from "./ContainerForm";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const MiB = 2 ** 20;
const GiB = 2 ** 30;

const lists = {
  "GET /status": () => json(200, { docker: "ok", admin: false, cpus: 4, memory: 8 * GiB }),
  "GET /volumes": () =>
    json(200, [
      {
        name: "web-data",
        driver: "local",
        mountpoint: "",
        createdAt: "",
        scope: "local",
        options: {},
        anonymous: false,
        usedBy: [],
      },
      {
        name: "f".repeat(64),
        driver: "local",
        mountpoint: "",
        createdAt: "",
        scope: "local",
        options: {},
        anonymous: true,
        usedBy: [],
      },
    ]),
  "GET /networks": () =>
    json(200, [
      {
        id: "b",
        name: "bridge",
        driver: "bridge",
        scope: "local",
        internal: false,
        predefined: true,
        containers: 0,
      },
      {
        id: "f",
        name: "front",
        driver: "bridge",
        scope: "local",
        internal: false,
        predefined: false,
        containers: 1,
      },
    ]),
};

function type(el: HTMLElement, value: string) {
  fireEvent.input(el, { target: { value } });
}

describe("ContainerCreate", () => {
  it("creates a container with a port, a volume, a bind mount and a variable", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /images/present": () => json(200, { present: true }),
      "POST /containers": () => json(201, { id: "new-id" }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "web");
    type(view.getByLabelText("Image"), "nginx:1.29");

    fireEvent.click(view.getByRole("button", { name: "Add port" }));
    type(view.getByLabelText("Host port"), "8080");
    type(view.getByLabelText("Container port"), "80");

    fireEvent.click(view.getByRole("button", { name: "Add volume" }));
    const volume = await view.findByLabelText("Volume name");
    // the list of volumes arrives after the select: choose once the option is there
    await waitFor(() => expect(volume.querySelector("option[value=web-data]")).toBeTruthy());
    fireEvent.change(volume, { target: { value: "web-data" } });
    type(view.getByLabelText("Path in the container"), "/usr/share/nginx/html");
    fireEvent.click(view.getByRole("button", { name: "Add volume" }));
    fireEvent.click(view.getAllByRole("radio", { name: "Bind mount" })[1]!);
    type(view.getByLabelText("Path on the host"), "/srv/conf");
    type(view.getAllByLabelText("Path in the container")[1]!, "/etc/nginx/conf.d");
    fireEvent.click(view.getAllByLabelText("read-only")[1]!);

    fireEvent.click(view.getByRole("button", { name: "Add variable" }));
    type(view.getByLabelText("Name", { selector: "[data-env] input" }), "TZ");
    type(view.getByLabelText("Value"), "Europe/Berlin");
    fireEvent.change(view.getByLabelText("Restart policy"), { target: { value: "unless-stopped" } });

    fireEvent.click(view.getByRole("button", { name: "Create container" }));
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
    const sent = calls.find((c) => c.method === "POST" && c.path === "/containers")?.body as Spec;
    expect(sent).toEqual({
      name: "web",
      image: "nginx:1.29",
      command: "",
      privileged: false,
      network: "bridge",
      start: true,
      ports: [{ hostIp: "", hostPort: 8080, containerPort: 80, protocol: "tcp" }],
      mounts: [
        { type: "volume", source: "web-data", target: "/usr/share/nginx/html", readOnly: false },
        { type: "bind", source: "/srv/conf", target: "/etc/nginx/conf.d", readOnly: true },
      ],
      env: [{ name: "TZ", value: "Europe/Berlin" }],
      labels: [],
      restart: { policy: "unless-stopped", maxRetries: 0 },
      // always sent: a missing "resources" would mean no limits (and remove the limits of an edited container)
      resources: { memory: 0, cpus: 0 },
    });
    expect(calls.find((c) => c.path.startsWith("/images/present"))?.path).toBe(
      "/images/present?ref=nginx%3A1.29",
    );
  });

  describe("labels", () => {
    /** The create form with name and image filled in, the image present, and the POST answering with answer. */
    async function filled(answer: () => Response = () => json(201, { id: "new-id" })) {
      stubEventSource();
      const { calls } = stubFetch({
        ...lists,
        "GET /images/present": () => json(200, { present: true }),
        "POST /containers": answer,
      });
      const view = mount(ContainerCreate, { path: "/containers/new" });
      type(await view.findByLabelText("Name"), "web");
      type(view.getByLabelText("Image"), "nginx:1.29");
      const sent = () => calls.find((c) => c.method === "POST" && c.path === "/containers")?.body as Spec;
      return { view, sent };
    }
    const rows = (view: ReturnType<typeof mount>) => [
      ...view.container.querySelectorAll<HTMLElement>("[data-label]"),
    ];
    const keys = (view: ReturnType<typeof mount>) => view.queryAllByLabelText("Key") as HTMLInputElement[];
    const values = (view: ReturnType<typeof mount>) =>
      view.queryAllByLabelText("Value", { selector: "[data-label] input" }) as HTMLInputElement[];

    it("has a section after the environment, with nothing in it but the button to add a label", async () => {
      const { view } = await filled();
      const titles = [...view.container.querySelectorAll("legend")].map((legend) => legend.textContent);
      expect(titles.indexOf("Labels")).toBe(titles.indexOf("Environment") + 1);
      expect(rows(view)).toHaveLength(0);
      expect(view.getByRole("button", { name: "Add label" })).toBeTruthy();
    });

    it("creates a container with labels, the keys without blanks around them and the values as typed", async () => {
      const { view, sent } = await filled();
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      type(keys(view)[0]!, "  com.example.App ");
      type(values(view)[0]!, " web ");
      type(keys(view)[1]!, "tier"); // its value stays empty
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().labels).toEqual([
        { key: "com.example.App", value: " web " },
        { key: "tier", value: "" },
      ]);
    });

    it("adds and removes rows, each remove button taking its own row", async () => {
      const { view } = await filled();
      for (const [key, value] of [
        ["a", "1"],
        ["b", "2"],
        ["c", "3"],
      ] as const) {
        fireEvent.click(view.getByRole("button", { name: "Add label" }));
        type(keys(view).at(-1)!, key);
        type(values(view).at(-1)!, value);
      }
      expect(keys(view).map((k) => k.value)).toEqual(["a", "b", "c"]);
      fireEvent.click(within(rows(view)[1]!).getByRole("button", { name: "Remove" }));
      await settle();
      expect(keys(view).map((k) => k.value)).toEqual(["a", "c"]);
      expect(values(view).map((v) => v.value)).toEqual(["1", "3"]);
      fireEvent.click(within(rows(view)[0]!).getByRole("button", { name: "Remove" }));
      fireEvent.click(within(rows(view)[0]!).getByRole("button", { name: "Remove" }));
      await settle();
      expect(rows(view)).toHaveLength(0);
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
    });

    it("keeps the other rows of the form when a label is removed", async () => {
      const { view, sent } = await filled();
      fireEvent.click(view.getByRole("button", { name: "Add variable" }));
      type(view.getByLabelText("Name", { selector: "[data-env] input" }), "TZ");
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      type(keys(view)[0]!, "gone");
      fireEvent.click(within(rows(view)[0]!).getByRole("button", { name: "Remove" }));
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().labels).toEqual([]);
      expect(sent().env).toEqual([{ name: "TZ", value: "" }]);
    });

    it("marks the key of the row the backend named, not the other fields", async () => {
      const { view } = await filled(() =>
        json(400, {
          error: {
            code: "invalid_container_spec",
            message: "labels.1.key is reserved for Docker",
            field: "labels.1.key",
          },
        }),
      );
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      type(keys(view)[0]!, "app");
      type(keys(view)[1]!, "com.docker.compose.project");
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await view.findByText("Invalid input: labels.1.key is reserved for Docker");
      const [first, second] = keys(view);
      expect(second!.classList.contains("input-error")).toBe(true);
      expect(second!.getAttribute("aria-invalid")).toBe("true");
      expect(first!.classList.contains("input-error")).toBe(false);
      expect(first!.getAttribute("aria-invalid")).toBe("false");
      expect(view.getByLabelText("Name").getAttribute("aria-invalid")).toBe("false");
    });
  });

  it("pulls a missing image first, with progress", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /images/present": () => json(200, { present: false }),
      "POST /pulls": () => json(202, { id: "p1", ref: "nginx:1.29" }),
      "POST /containers": () => json(201, { id: "new-id" }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "web");
    type(view.getByLabelText("Image"), "nginx:1.29");
    fireEvent.click(view.getByRole("button", { name: "Create container" }));
    expect(await view.findByRole("dialog", { name: "Pulling nginx:1.29" })).toBeTruthy();
    expect(calls.some((c) => c.path === "/containers")).toBe(false);
    sources.instances
      .find((s) => s.url.endsWith("/pulls/p1/events"))
      ?.emit("done", JSON.stringify({ ref: "nginx:1.29" }));
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
    expect(calls.find((c) => c.path === "/pulls")?.body).toEqual({ ref: "nginx:1.29" });
  });

  it("shows which field the backend refused", async () => {
    stubEventSource();
    stubFetch({
      ...lists,
      "GET /images/present": () => json(200, { present: true }),
      "POST /containers": () =>
        json(400, {
          error: { code: "invalid_container_spec", message: "name may only contain …", field: "name" },
        }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "a b");
    type(view.getByLabelText("Image"), "nginx");
    fireEvent.click(view.getByRole("button", { name: "Create container" }));
    expect(await view.findByText("Invalid input: name may only contain …")).toBeTruthy();
    const name = view.getByLabelText("Name");
    expect(name.getAttribute("aria-invalid")).toBe("true");
    expect(name.classList.contains("input-error")).toBe(true);
    const image = view.getByLabelText("Image");
    expect(image.getAttribute("aria-invalid")).toBe("false");
    expect(image.classList.contains("input-error")).toBe(false);
  });

  it("marks the row the backend named, not the others", async () => {
    stubEventSource();
    stubFetch({
      ...lists,
      "GET /images/present": () => json(200, { present: true }),
      "POST /containers": () =>
        json(400, {
          error: {
            code: "invalid_container_spec",
            message: "ports.1.hostPort is used twice",
            field: "ports.1.hostPort",
          },
        }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "web");
    type(view.getByLabelText("Image"), "nginx");
    fireEvent.click(view.getByRole("button", { name: "Add port" }));
    fireEvent.click(view.getByRole("button", { name: "Add port" }));
    const hostPorts = view.getAllByLabelText("Host port");
    const containerPorts = view.getAllByLabelText("Container port");
    type(hostPorts[0]!, "8080");
    type(containerPorts[0]!, "80");
    type(hostPorts[1]!, "8080");
    type(containerPorts[1]!, "81");
    fireEvent.click(view.getByRole("button", { name: "Create container" }));
    await view.findByText("Invalid input: ports.1.hostPort is used twice");
    expect(hostPorts[1]!.classList.contains("input-error")).toBe(true);
    expect(hostPorts[1]!.getAttribute("aria-invalid")).toBe("true");
    expect(hostPorts[0]!.classList.contains("input-error")).toBe(false);
    expect(containerPorts[1]!.classList.contains("input-error")).toBe(false);
  });

  it("offers the named volumes with their driver, and a new volume with a name", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /images/present": () => json(200, { present: true }),
      "POST /containers": () => json(201, { id: "new-id" }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "web");
    type(view.getByLabelText("Image"), "nginx:1.29");
    fireEvent.click(view.getByRole("button", { name: "Add volume" }));
    const select = (await view.findByLabelText("Volume name")) as HTMLSelectElement;
    await waitFor(() => expect(select.options.length).toBe(3));
    expect([...select.options].map((o) => o.textContent)).toEqual(["–", "web-data (local)", "New volume …"]);
    expect(select.value).toBe("");
    expect(view.queryByLabelText("Name of the new volume")).toBeNull();
    fireEvent.change(select, { target: { value: "__new__" } });
    type(view.getByLabelText("Name of the new volume"), "cache");
    type(view.getByLabelText("Path in the container"), "/cache");
    fireEvent.click(view.getByRole("button", { name: "Create container" }));
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
    const sent = calls.find((c) => c.method === "POST" && c.path === "/containers")?.body as Spec;
    expect(sent.mounts).toEqual([{ type: "volume", source: "cache", target: "/cache", readOnly: false }]);
  });

  it("chooses a new volume when there is none yet, also when the list comes late", async () => {
    stubEventSource();
    let answer: (r: Response) => void = () => {};
    stubFetch({
      ...lists,
      "GET /volumes": () => new Promise<Response>((resolve) => (answer = resolve)),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    fireEvent.click(await view.findByRole("button", { name: "Add volume" }));
    answer(json(200, []));
    await waitFor(() => expect(view.getByLabelText("Name of the new volume")).toBeTruthy());
    expect((view.getByLabelText("Volume name") as HTMLSelectElement).value).toBe("__new__");
  });

  it("falls back to a name field when the volumes cannot be loaded", async () => {
    stubEventSource();
    stubFetch({
      ...lists,
      "GET /volumes": () => json(503, { error: { code: "docker_unavailable", message: "gone" } }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    fireEvent.click(await view.findByRole("button", { name: "Add volume" }));
    // the select is there while the list loads, so wait for the error first
    const message = await view.findByText(/could not be loaded/);
    const field = view.getByLabelText("Volume name");
    expect(field.tagName).toBe("INPUT");
    expect(view.getAllByLabelText("Volume name")).toEqual([field]);
    expect(message.id).not.toBe("");
    expect(field.getAttribute("aria-describedby")).toBe(message.id);
  });

  // typed one event after the other, as a user does: the new volume must not end with the first character
  it.each<[string, () => Response, boolean]>([
    ["there is no named volume", () => json(200, []), false],
    ["the user chose New volume", lists["GET /volumes"], true],
  ])("keeps a new volume while its name is typed, when %s", async (_when, volumeList, choose) => {
    stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /volumes": volumeList,
      "GET /images/present": () => json(200, { present: true }),
      "POST /containers": () => json(201, { id: "new-id" }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "web");
    type(view.getByLabelText("Image"), "nginx:1.29");
    fireEvent.click(view.getByRole("button", { name: "Add volume" }));
    const select = (await view.findByLabelText("Volume name")) as HTMLSelectElement;
    if (choose) {
      await waitFor(() => expect(select.options.length).toBe(3));
      fireEvent.change(select, { target: { value: "__new__" } });
    }
    const field = await view.findByLabelText("Name of the new volume");
    type(field, "c");
    type(field, "ca");
    await settle();
    expect(view.getByLabelText("Name of the new volume")).toBe(field);
    expect((field as HTMLInputElement).value).toBe("ca");
    expect(select.value).toBe("__new__");
    expect([...select.options].map((o) => o.textContent)).not.toContain("ca (not found)");
    type(view.getByLabelText("Path in the container"), "/cache");
    fireEvent.click(view.getByRole("button", { name: "Create container" }));
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
    const sent = calls.find((c) => c.method === "POST" && c.path === "/containers")?.body as Spec;
    expect(sent.mounts).toEqual([{ type: "volume", source: "ca", target: "/cache", readOnly: false }]);
  });

  describe("two volume rows, the first one removed", () => {
    async function twoRows() {
      stubEventSource();
      stubFetch(lists);
      const view = mount(ContainerCreate, { path: "/containers/new" });
      fireEvent.click(await view.findByRole("button", { name: "Add volume" }));
      fireEvent.click(view.getByRole("button", { name: "Add volume" }));
      const selects = () => view.getAllByLabelText("Volume name") as HTMLSelectElement[];
      await waitFor(() => {
        expect(selects()).toHaveLength(2);
        expect(selects().every((s) => s.options.length === 3)).toBe(true);
      });
      return { view, selects };
    }

    it("keeps an existing volume of the second row, which was not new like the first", async () => {
      const { view, selects } = await twoRows();
      fireEvent.change(selects()[0]!, { target: { value: "__new__" } });
      type(view.getByLabelText("Name of the new volume"), "cache");
      fireEvent.change(selects()[1]!, { target: { value: "web-data" } });
      fireEvent.click(view.getAllByRole("button", { name: "Remove" })[0]!);
      await settle();
      expect(selects().map((s) => s.value)).toEqual(["web-data"]);
      expect(selects()[0]!.selectedOptions[0]?.textContent).toBe("web-data (local)");
      expect(view.queryByLabelText("Name of the new volume")).toBeNull();
    });

    it("keeps the new volume of the second row with its name, not as not found", async () => {
      const { view, selects } = await twoRows();
      fireEvent.change(selects()[0]!, { target: { value: "web-data" } });
      fireEvent.change(selects()[1]!, { target: { value: "__new__" } });
      type(view.getByLabelText("Name of the new volume"), "cache");
      fireEvent.click(view.getAllByRole("button", { name: "Remove" })[0]!);
      await settle();
      expect(selects().map((s) => s.value)).toEqual(["__new__"]);
      expect(selects()[0]!.selectedOptions[0]?.textContent).toBe("New volume …");
      expect((view.getByLabelText("Name of the new volume") as HTMLInputElement).value).toBe("cache");
    });
  });

  it("leaves the form usable when the pull dialog is closed (the Close button, Escape)", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /images/present": () => json(200, { present: false }),
      "POST /pulls": () => json(202, { id: "p1", ref: "nginx:1.29" }),
      "POST /containers": () => json(201, { id: "new-id" }),
    });
    const view = mount(ContainerCreate, { path: "/containers/new" });
    type(await view.findByLabelText("Name"), "web");
    type(view.getByLabelText("Image"), "nginx:1.29");
    const submit = view.getByRole("button", { name: "Create container" }) as HTMLButtonElement;
    for (const leave of [
      () => fireEvent.click(view.getByRole("button", { name: "Close" })),
      () => fireEvent.keyDown(document, { key: "Escape" }),
    ]) {
      fireEvent.click(submit);
      expect(await view.findByRole("dialog", { name: "Pulling nginx:1.29" })).toBeTruthy();
      expect(submit.disabled).toBe(true);
      leave();
      await waitFor(() => expect(view.queryByRole("dialog")).toBeNull());
      expect(submit.disabled).toBe(false);
      expect(view.queryByRole("alert")).toBeNull();
    }
    // the streams of both pulls are closed and the form stayed where it was
    const pulls = sources.instances.filter((s) => s.url.includes("/pulls/"));
    expect(pulls).toHaveLength(2);
    expect(pulls.every((s) => s.readyState === 2)).toBe(true);
    expect(calls.some((c) => c.path === "/containers")).toBe(false);
    expect(view.history.get()).toBe("/x/docker/containers/new");
  });

  it("does not warn about a recreation when creating", async () => {
    stubEventSource();
    stubFetch(lists);
    const view = mount(ContainerCreate, { path: "/containers/new" });
    fireEvent.click(await view.findByLabelText("Pull the image again before saving"));
    expect(view.queryByText(/is recreated/)).toBeNull();
  });

  describe("resources", () => {
    /** The create form with name and image filled in, the image present, and the POST answering with answer. */
    async function filled(answer: () => Response, routes: Record<string, () => Response> = {}) {
      stubEventSource();
      const { calls } = stubFetch({
        ...lists,
        ...routes,
        "GET /images/present": () => json(200, { present: true }),
        "POST /containers": answer,
      });
      const view = mount(ContainerCreate, { path: "/containers/new" });
      type(await view.findByLabelText("Name"), "web");
      type(view.getByLabelText("Image"), "nginx:1.29");
      const sent = () => calls.find((c) => c.method === "POST" && c.path === "/containers")?.body as Spec;
      return { view, sent };
    }

    it("sends a memory limit in MiB or GiB and a CPU limit, and names the host's limits", async () => {
      const { view, sent } = await filled(() =>
        json(201, { id: "new-id", warnings: ["Your kernel does not support memory limit"] }),
      );
      type(view.getByLabelText("Memory limit"), "512");
      fireEvent.change(view.getByLabelText("Unit"), { target: { value: "GiB" } }); // the number stays: 512 GiB
      fireEvent.change(view.getByLabelText("Unit"), { target: { value: "MiB" } });
      type(view.getByLabelText("CPU limit (cores)"), "1.5");
      expect(await view.findByText(/at most 8 GiB/)).toBeTruthy();
      expect(view.getByText(/at most 4 cores/)).toBeTruthy();
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().resources).toEqual({ memory: 512 * MiB, cpus: 1.5 });
      expect(view.toasts.shown.map((s) => s.message)).toEqual([
        "Your kernel does not support memory limit",
        "web was created.",
      ]);
    });

    it("converts the memory to bytes with the unit that is chosen when the form is sent", async () => {
      const { view, sent } = await filled(() => json(201, { id: "new-id" }));
      type(view.getByLabelText("Memory limit"), "2");
      fireEvent.change(view.getByLabelText("Unit"), { target: { value: "GiB" } });
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().resources).toEqual({ memory: 2 * GiB, cpus: 0 });
    });

    it("accepts fractions (0.25 cores, 1.5 GiB): the browser's own number check does not stop the form", async () => {
      const { view, sent } = await filled(() => json(201, { id: "new-id" }));
      type(view.getByLabelText("Memory limit"), "1.5");
      fireEvent.change(view.getByLabelText("Unit"), { target: { value: "GiB" } });
      type(view.getByLabelText("CPU limit (cores)"), "0.25");
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().resources).toEqual({ memory: 1.5 * GiB, cpus: 0.25 });
    });

    it("sends no limit for an empty field", async () => {
      const { view, sent } = await filled(() => json(201, { id: "new-id" }));
      type(view.getByLabelText("Memory limit"), "512");
      type(view.getByLabelText("CPU limit (cores)"), "2");
      type(view.getByLabelText("Memory limit"), "");
      type(view.getByLabelText("CPU limit (cores)"), "");
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().resources).toEqual({ memory: 0, cpus: 0 });
    });

    // a value the backend refuses must reach it (and come back as its field error), not become "no limit" on the way
    it.each<[string, string, string, number, number]>([
      ["a memory limit below the minimum", "Memory limit", "1", 2 ** 20, 0],
      ["a memory limit that is less than one byte", "Memory limit", "0.0000001", 1, 0],
      ["a memory limit beyond the range of integers", "Memory limit", "1e30", Number.MAX_SAFE_INTEGER, 0],
      ["a memory limit that overflows in bytes", "Memory limit", "1e303", Number.MAX_SAFE_INTEGER, 0],
      ["a CPU limit no host has", "CPU limit (cores)", "1e30", 0, 1e30],
    ])("passes on %s", async (_what, label, text, memory, cpus) => {
      const { view, sent } = await filled(() => json(201, { id: "new-id" }));
      type(view.getByLabelText(label), text);
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().resources).toEqual({ memory, cpus });
    });

    it("leaves a CPU limit below 0.01 to the browser's own check, which names the minimum", async () => {
      const { view, sent } = await filled(() => json(201, { id: "new-id" }));
      const cpus = view.getByLabelText("CPU limit (cores)") as HTMLInputElement;
      type(cpus, "0.001");
      expect(cpus.validity.rangeUnderflow).toBe(true);
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await settle();
      expect(sent()).toBeUndefined();
      expect(view.history.get()).toBe("/x/docker/containers/new");
    });

    it.each([
      [
        "resources.memory",
        "Memory limit",
        "CPU limit (cores)",
        "must be at least 6 MiB, or empty for no limit",
      ],
      [
        "resources.cpus",
        "CPU limit (cores)",
        "Memory limit",
        "must not be more than 4, the number of CPUs of the host",
      ],
    ])("marks the limit the backend refused (%s), not the other", async (field, marked, other, message) => {
      const { view } = await filled(() =>
        json(400, { error: { code: "invalid_container_spec", message, field } }),
      );
      type(view.getByLabelText("Memory limit"), "1");
      type(view.getByLabelText("CPU limit (cores)"), "9");
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      expect(await view.findByText(`Invalid input: ${message}`)).toBeTruthy();
      const field1 = view.getByLabelText(marked);
      expect(field1.getAttribute("aria-invalid")).toBe("true");
      expect(field1.classList.contains("input-error")).toBe(true);
      const field2 = view.getByLabelText(other);
      expect(field2.getAttribute("aria-invalid")).toBe("false");
      expect(field2.classList.contains("input-error")).toBe(false);
      expect(view.history.get()).toBe("/x/docker/containers/new");
    });

    it("leaves what is typed alone: 1.50 stays 1.50 although the limit is 1.5", async () => {
      // a browser reports "" for a half-typed "1." and clears the field when it is written back
      const { view } = await filled(() => json(201, { id: "new-id" }));
      const cpus = view.getByLabelText("CPU limit (cores)") as HTMLInputElement;
      const memory = view.getByLabelText("Memory limit") as HTMLInputElement;
      type(cpus, "1.50");
      type(memory, "512.0");
      await settle();
      expect(cpus.value).toBe("1.50");
      expect(memory.value).toBe("512.0");
    });

    it("names no maximum when Docker gives no figures for the host", async () => {
      stubEventSource();
      for (const status of [
        () => json(503, { error: { code: "docker_unavailable", message: "gone" } }),
        () => json(200, { docker: "ok", admin: false }),
      ]) {
        stubFetch({ ...lists, "GET /status": status });
        const view = mount(ContainerCreate, { path: "/containers/new" });
        await view.findByLabelText("Name");
        await settle();
        expect(view.getAllByText("empty: no limit")).toHaveLength(2);
        expect(view.queryByText(/at most/)).toBeNull();
        cleanup();
      }
    });

    describe("while /status is on its way", () => {
      const host = { docker: "ok", admin: false, cpus: 4, memory: 8 * GiB };
      /** The form with a /status the test answers by hand. */
      async function waiting() {
        stubEventSource();
        let answer!: (r: Response) => void;
        stubFetch({ ...lists, "GET /status": () => new Promise<Response>((resolve) => (answer = resolve)) });
        const view = mount(ContainerCreate, { path: "/containers/new" });
        await view.findByLabelText("Name");
        await settle();
        return { view, answer: (r: Response) => answer(r) };
      }

      it("shows no hint, and the one with the maximum when the answer comes", async () => {
        const { view, answer } = await waiting();
        expect(view.queryByText(/empty: no limit/)).toBeNull();
        answer(json(200, host));
        expect(await view.findByText("empty: no limit · at most 8 GiB")).toBeTruthy();
        expect(view.getByText("empty: no limit · at most 4 cores")).toBeTruthy();
      });

      it("shows the hints without maximum when it fails", async () => {
        const { view, answer } = await waiting();
        expect(view.queryByText(/empty: no limit/)).toBeNull();
        answer(json(503, { error: { code: "docker_unavailable", message: "gone" } }));
        await waitFor(() => expect(view.getAllByText("empty: no limit")).toHaveLength(2));
        expect(view.queryByText(/at most/)).toBeNull();
      });
    });

    it("ties each hint to its field", async () => {
      const { view } = await filled(() => json(201, { id: "new-id" }));
      for (const [label, hint] of [
        ["Memory limit", "empty: no limit · at most 8 GiB"],
        ["CPU limit (cores)", "empty: no limit · at most 4 cores"],
      ] as const) {
        const text = await view.findByText(hint);
        const id = view.getByLabelText(label).getAttribute("aria-describedby");
        expect(id).toBeTruthy();
        expect(text.id).toBe(id);
      }
    });

    it.each<["en" | "de", number, string]>([
      ["en", 1, "empty: no limit · at most 1 core"],
      ["en", 2, "empty: no limit · at most 2 cores"],
      ["de", 1, "leer: kein Limit · höchstens 1 Kern"],
      ["de", 2, "leer: kein Limit · höchstens 2 Kerne"],
    ])("writes the host's CPUs in the plural form (%s, %d)", async (locale, cpus, text) => {
      stubEventSource();
      stubFetch({
        ...lists,
        "GET /status": () => json(200, { docker: "ok", admin: false, cpus, memory: 8 * GiB }),
      });
      const view = mount(ContainerCreate, { path: "/containers/new", locale });
      expect(await view.findByText(text)).toBeTruthy();
    });

    it("warns in the memory hint when the limit is above the host's memory, and blocks nothing", async () => {
      const { view, sent } = await filled(() => json(201, { id: "new-id" }));
      const hint = await view.findByText("empty: no limit · at most 8 GiB");
      const memory = view.getByLabelText("Memory limit");
      const unit = view.getByLabelText("Unit");
      const warns = () => hint.classList.contains("text-warning");
      expect(warns()).toBe(false);
      type(memory, "9"); // 9 MiB
      expect(warns()).toBe(false);
      fireEvent.change(unit, { target: { value: "GiB" } }); // the number stays: 9 GiB is what is sent now
      expect(warns()).toBe(true);
      expect(hint.classList.contains("text-base-content/70")).toBe(false);
      type(memory, "8"); // exactly the host's memory
      expect(warns()).toBe(false);
      expect(hint.classList.contains("text-base-content/70")).toBe(true);
      type(memory, "9");
      expect(warns()).toBe(true);
      type(memory, "");
      expect(warns()).toBe(false);
      type(memory, "9");
      fireEvent.click(view.getByRole("button", { name: "Create container" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().resources.memory).toBe(9 * GiB);
    });

    it("sets the limits in a form section of its own, whose fields are numbers a browser can step", async () => {
      const { view } = await filled(() => json(201, { id: "new-id" }));
      const section = view.getByRole("group", { name: "Resources" });
      const memory = view.getByLabelText("Memory limit") as HTMLInputElement;
      const cpus = view.getByLabelText("CPU limit (cores)") as HTMLInputElement;
      expect(section.contains(memory)).toBe(true);
      expect(section.contains(cpus)).toBe(true);
      expect(section.contains(view.getByLabelText("Unit"))).toBe(true);
      expect([memory.type, memory.min, memory.step]).toEqual(["number", "0", "any"]);
      // min is the base of the steps: 0.01 with the step 0.1 would make 0.5, 1.5 and 2 steps a browser refuses
      expect([cpus.type, cpus.min, cpus.step]).toEqual(["number", "0.01", "any"]);
    });
  });
});

describe("ContainerEdit", () => {
  const spec: Spec = {
    name: "web",
    image: "nginx:1.29",
    command: "",
    network: "bridge",
    privileged: false,
    resources: { memory: 0, cpus: 0 },
    ports: [{ hostIp: "", hostPort: 8080, containerPort: 80, protocol: "tcp" }],
    mounts: [{ type: "volume", source: "a".repeat(64), target: "/cache", readOnly: false, anonymous: true }],
    env: [],
    labels: [],
    restart: { policy: "no", maxRetries: 0 },
  };

  it("tells when saving recreates the container, and saves", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /containers/web": () => json(200, { ...info, spec }),
      "GET /images/present": () => json(200, { present: true }),
      "PUT /containers/aaaaaaaaaaaaaaaa": () => json(200, { id: "new-id", recreated: true }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    const name = (await view.findByLabelText("Name")) as HTMLInputElement;
    expect(name.value).toBe("web");
    expect(view.getByText("anonymous")).toBeTruthy();
    type(name, "web2");
    await settle();
    expect(view.queryByText(/recreate the container/)).toBeNull();
    type(view.getByLabelText("Host port"), "9090");
    expect(await view.findByText(/These changes recreate the container/)).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
    const sent = calls.find((c) => c.method === "PUT")?.body as Spec;
    expect(sent.name).toBe("web2");
    expect(sent.ports[0]?.hostPort).toBe(9090);
    expect(sent.mounts).toEqual([
      { type: "volume", source: "a".repeat(64), target: "/cache", readOnly: false },
    ]);
    expect(Object.keys(sent.mounts[0]!)).not.toContain("anonymous");
    expect(view.toasts.shown.map((s) => s.message)).toContain("web2 was recreated.");
  });

  it("shows a spinner while saving, and that stopping the old container takes a while", async () => {
    stubEventSource();
    let release!: () => void;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    stubFetch({
      ...lists,
      "GET /containers/web": () => json(200, { ...info, spec }),
      "GET /images/present": () => json(200, { present: true }),
      "PUT /containers/aaaaaaaaaaaaaaaa": async () => {
        await gate;
        return json(200, { id: "new-id", recreated: true });
      },
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    await view.findByLabelText("Name");
    const stopHint = "Saving stops the old container first (if it runs), which can take up to 10 seconds.";
    const save = view.getByRole("button", { name: "Save" }) as HTMLButtonElement;
    expect(save.querySelector(".loading")).toBeNull();
    // only a recreation stops the old container
    type(view.getByLabelText("Name"), "web2");
    await settle();
    expect(view.queryByText(stopHint)).toBeNull();
    type(view.getByLabelText("Host port"), "9090");
    expect((await view.findByText(stopHint)).closest("[role=status]")).toBeTruthy();
    fireEvent.click(save);
    await waitFor(() => expect(save.disabled).toBe(true));
    expect(save.querySelector(".loading.loading-spinner.loading-sm")).toBeTruthy();
    expect(view.getByRole("button", { name: "Save" })).toBe(save);
    release();
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
  });

  it("knows which changes need a recreation", () => {
    expect(needsRecreate(spec, { ...spec, name: "x", restart: { policy: "always", maxRetries: 0 } })).toBe(
      false,
    );
    expect(needsRecreate(spec, { ...spec, image: "nginx:1.30" })).toBe(true);
    expect(needsRecreate(spec, { ...spec, env: [{ name: "A", value: "1" }] })).toBe(true);
    expect(needsRecreate(spec, { ...spec, labels: [{ key: "app", value: "web" }] })).toBe(true);
    const limited = { ...spec, resources: { memory: 256 * MiB, cpus: 1 } };
    expect(needsRecreate(limited, { ...limited, resources: { memory: 512 * MiB, cpus: 2 } })).toBe(false);
    expect(needsRecreate(limited, { ...limited, resources: { memory: 0, cpus: 1 } })).toBe(true);
    expect(needsRecreate(limited, { ...limited, resources: { memory: 256 * MiB, cpus: 0 } })).toBe(true);
  });

  it("sees a CPU limit as the backend does: in whole nanoCPUs", () => {
    const limited = (cpus: number) => ({ ...spec, resources: { memory: 0, cpus } });
    const sum = 0.1 + 0.2; // 0.30000000000000004: the same limit as 0.3
    expect(sum).not.toBe(0.3);
    expect(needsRecreate(limited(0.3), limited(sum))).toBe(false);
    expect(needsRecreate(limited(sum), limited(0.3))).toBe(false);
    // a limit that rounds to no nanoCPU is a removed limit (the backend refuses it, but it is no limit either) ...
    expect(needsRecreate(limited(1), limited(4e-10))).toBe(true);
    // ... one that rounds up to a nanoCPU is a limit
    expect(needsRecreate(limited(1), limited(6e-10))).toBe(false);
    // nothing to remove without a limit before
    expect(needsRecreate(limited(0), limited(4e-10))).toBe(false);
    expect(needsRecreate(limited(0), limited(2))).toBe(false);
    // and a limit that rounds to nothing before is none to remove
    expect(needsRecreate(limited(4e-10), limited(0))).toBe(false);
  });

  it("does not count the order of the rows or blanks around a value as a change", () => {
    const rich: Spec = {
      ...spec,
      ports: [
        { hostIp: "", hostPort: 8080, containerPort: 80, protocol: "tcp" },
        { hostIp: "127.0.0.1", hostPort: 53, containerPort: 53, protocol: "udp" },
      ],
      mounts: [
        { type: "volume", source: "web-data", target: "/data", readOnly: false },
        { type: "bind", source: "/srv/conf", target: "/etc/conf", readOnly: true },
      ],
      env: [
        { name: "TZ", value: "UTC" },
        { name: "A", value: "1" },
      ],
    };
    const reversed: Spec = {
      ...rich,
      ports: [...rich.ports].reverse(),
      mounts: [...rich.mounts].reverse(),
      env: [...rich.env].reverse(),
    };
    expect(needsRecreate(rich, reversed)).toBe(false);
    expect(needsRecreate(rich, { ...reversed, image: " nginx:1.29 ", command: " " })).toBe(false);
    expect(
      needsRecreate(rich, {
        ...reversed,
        mounts: reversed.mounts.map((m) => ({ ...m, source: ` ${m.source} `, target: `${m.target} ` })),
        env: reversed.env.map((e) => ({ ...e, name: ` ${e.name}` })),
      }),
    ).toBe(false);
    // but a changed row is a change, wherever it stands
    expect(
      needsRecreate(rich, {
        ...reversed,
        ports: [reversed.ports[0]!, { ...reversed.ports[1]!, hostPort: 9090 }],
      }),
    ).toBe(true);
    expect(
      needsRecreate(rich, {
        ...reversed,
        mounts: reversed.mounts.map((m) => ({ ...m, readOnly: !m.readOnly })),
      }),
    ).toBe(true);
    expect(needsRecreate(rich, { ...reversed, env: [reversed.env[0]!, { name: "TZ", value: "CET" }] })).toBe(
      true,
    );
    expect(needsRecreate(rich, { ...rich, ports: rich.ports.slice(0, 1) })).toBe(true);
  });

  // Docker cannot change the labels of a container: a change of them recreates it, the order of the rows does not
  it("counts a change of the labels, but not their order or blanks around a key", () => {
    const labelled: Spec = {
      ...spec,
      labels: [
        { key: "tier", value: "front" },
        { key: "app", value: "web" },
      ],
    };
    expect(needsRecreate(labelled, { ...labelled, labels: [...labelled.labels].reverse() })).toBe(false);
    expect(
      needsRecreate(labelled, {
        ...labelled,
        labels: labelled.labels.map((l) => ({ ...l, key: `  ${l.key} ` })),
      }),
    ).toBe(false);
    expect(needsRecreate(spec, { ...spec, labels: [] })).toBe(false);
    // added, removed, another value (blanks in a value count), another key (the case counts), an empty value
    expect(
      needsRecreate(labelled, { ...labelled, labels: [...labelled.labels, { key: "stage", value: "" }] }),
    ).toBe(true);
    expect(needsRecreate(labelled, { ...labelled, labels: labelled.labels.slice(1) })).toBe(true);
    expect(needsRecreate(labelled, { ...labelled, labels: [] })).toBe(true);
    expect(
      needsRecreate(labelled, { ...labelled, labels: [labelled.labels[0]!, { key: "app", value: "web " }] }),
    ).toBe(true);
    expect(
      needsRecreate(labelled, { ...labelled, labels: [labelled.labels[0]!, { key: "App", value: "web" }] }),
    ).toBe(true);
    expect(
      needsRecreate(labelled, { ...labelled, labels: [{ key: "tier", value: "" }, labelled.labels[1]!] }),
    ).toBe(true);
    // a name and a limit change next to it do not hide it
    expect(
      needsRecreate(labelled, {
        ...labelled,
        name: "x",
        labels: labelled.labels.slice(1),
        resources: { memory: 1, cpus: 1 },
      }),
    ).toBe(true);
  });

  describe("labels", () => {
    const labelled: Spec = {
      ...spec,
      labels: [
        { key: "app", value: "web" },
        { key: "tier", value: "front" },
      ],
    };
    async function open(answer?: () => Response) {
      stubEventSource();
      const { calls } = stubFetch({
        ...lists,
        "GET /containers/web": () => json(200, { ...info, spec: labelled }),
        "GET /images/present": () => json(200, { present: true }),
        "PUT /containers/aaaaaaaaaaaaaaaa": answer ?? (() => json(200, { id: "new-id", recreated: true })),
      });
      const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
      await view.findByLabelText("Name");
      const rows = () => [...view.container.querySelectorAll<HTMLElement>("[data-label]")];
      const keys = () => view.queryAllByLabelText("Key") as HTMLInputElement[];
      const values = () =>
        view.queryAllByLabelText("Value", { selector: "[data-label] input" }) as HTMLInputElement[];
      const sent = () => calls.find((c) => c.method === "PUT")?.body as Spec;
      return { view, rows, keys, values, sent };
    }
    const notice = /These changes recreate the container/;

    it("shows the labels of the container, and no notice while they are as they were", async () => {
      const { view, keys, values } = await open();
      expect(keys().map((k) => k.value)).toEqual(["app", "tier"]);
      expect(values().map((v) => v.value)).toEqual(["web", "front"]);
      await settle();
      expect(view.queryByText(notice)).toBeNull();
    });

    it("tells that saving recreates the container as soon as a label differs, and not again once it is as it was", async () => {
      const { view, keys, values, rows } = await open();
      type(values()[0]!, "api");
      expect(await view.findByText(notice)).toBeTruthy();
      type(values()[0]!, "web");
      await waitFor(() => expect(view.queryByText(notice)).toBeNull());
      type(keys()[1]!, "layer");
      expect(await view.findByText(notice)).toBeTruthy();
      type(keys()[1]!, "tier");
      await waitFor(() => expect(view.queryByText(notice)).toBeNull());
      fireEvent.click(within(rows()[1]!).getByRole("button", { name: "Remove" }));
      expect(await view.findByText(notice)).toBeTruthy();
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      type(keys()[1]!, "tier");
      type(values()[1]!, "front");
      await waitFor(() => expect(view.queryByText(notice)).toBeNull()); // the same labels again
    });

    it("recreates for a label alone, and sends the labels as they are in the form", async () => {
      const { view, keys, values, rows, sent } = await open();
      fireEvent.click(within(rows()[0]!).getByRole("button", { name: "Remove" }));
      fireEvent.click(view.getByRole("button", { name: "Add label" }));
      type(keys()[1]!, " stage ");
      type(values()[1]!, "prod");
      expect(await view.findByText(notice)).toBeTruthy();
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/new-id"));
      expect(sent().labels).toEqual([
        { key: "tier", value: "front" },
        { key: "stage", value: "prod" },
      ]);
      expect(view.toasts.shown.map((s) => s.message)).toContain("web was recreated.");
    });

    it("marks the key of the label the backend refused", async () => {
      const { view, keys } = await open(() =>
        json(400, {
          error: {
            code: "invalid_container_spec",
            message: "labels.0.key is used twice",
            field: "labels.0.key",
          },
        }),
      );
      type(keys()[1]!, "app");
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      await view.findByText("Invalid input: labels.0.key is used twice");
      expect(keys()[0]!.classList.contains("input-error")).toBe(true);
      expect(keys()[0]!.getAttribute("aria-invalid")).toBe("true");
      expect(keys()[1]!.classList.contains("input-error")).toBe(false);
    });
  });

  it("warns that pulling again may recreate the container, only while the box is ticked", async () => {
    stubEventSource();
    stubFetch({ ...lists, "GET /containers/web": () => json(200, { ...info, spec }) });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    const reload = await view.findByLabelText("Pull the image again before saving");
    const hint = "If the pull brings a newer image, the container is recreated.";
    expect(view.queryByText(hint)).toBeNull();
    fireEvent.click(reload);
    expect(view.getByText(hint)).toBeTruthy();
    fireEvent.click(reload);
    expect(view.queryByText(hint)).toBeNull();
  });

  it("explains the empty host port of a ranged port in a visible line the fields refer to", async () => {
    stubEventSource();
    stubFetch({
      ...lists,
      "GET /containers/web": () =>
        json(200, {
          ...info,
          spec: {
            ...spec,
            ports: [
              { hostIp: "", hostPort: 0, containerPort: 80, protocol: "tcp" },
              { hostIp: "", hostPort: 8081, containerPort: 81, protocol: "tcp" },
            ],
          },
        }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    const hostPorts = (await view.findAllByLabelText("Host port")) as HTMLInputElement[];
    expect(hostPorts).toHaveLength(2);
    expect(hostPorts[0]!.value).toBe("");
    expect(hostPorts[0]!.placeholder).toBe("automatic");
    const hint = view.getByText(
      "Empty: Docker picks the port. A port range the container already has stays as it is.",
    );
    expect(view.getAllByText(/Docker picks the port/)).toHaveLength(1); // one line for all rows
    for (const field of hostPorts) expect(field.getAttribute("aria-describedby")).toBe(hint.id);
    expect(hint.id).not.toBe("");
  });

  it("shows no range hint without a port", async () => {
    stubEventSource();
    stubFetch({
      ...lists,
      "GET /containers/web": () => json(200, { ...info, spec: { ...spec, ports: [] } }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    await view.findByLabelText("Name");
    expect(view.queryByText(/Docker picks the port/)).toBeNull();
  });

  it("shows an anonymous volume with its full hash, and keeps its type", async () => {
    stubEventSource();
    stubFetch({ ...lists, "GET /containers/web": () => json(200, { ...info, spec }) });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    const name = (await view.findByLabelText("Volume name")) as HTMLInputElement;
    expect(name.value).toBe("a".repeat(12));
    expect(name.readOnly).toBe(true);
    expect(name.title).toBe("a".repeat(64));
    for (const radio of view.getAllByRole("radio")) expect((radio as HTMLInputElement).disabled).toBe(true);
  });

  it("shows a volume that is gone as not found and keeps it", async () => {
    stubEventSource();
    const gone: Spec = {
      ...spec,
      mounts: [{ type: "volume", source: "old-data", target: "/data", readOnly: false }],
    };
    stubFetch({
      ...lists,
      "GET /containers/web": () => json(200, { ...info, spec: gone }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    const select = (await view.findByLabelText("Volume name")) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("old-data"));
    expect(select.selectedOptions[0]?.textContent).toBe("old-data (not found)");
  });

  describe("a container with a named volume while the list of volumes is still on its way", () => {
    const withVolume: Spec = {
      ...spec,
      mounts: [{ type: "volume", source: "web-data", target: "/data", readOnly: false }],
    };

    it("selects the volume as soon as the list arrives", async () => {
      stubEventSource();
      let answer: (r: Response) => void = () => {};
      stubFetch({
        ...lists,
        "GET /volumes": () => new Promise<Response>((resolve) => (answer = resolve)),
        "GET /containers/web": () => json(200, { ...info, spec: withVolume }),
      });
      const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
      const select = (await view.findByLabelText("Volume name")) as HTMLSelectElement;
      answer(lists["GET /volumes"]());
      await waitFor(() => expect(select.options.length).toBe(3));
      expect(select.value).toBe("web-data");
      expect(select.selectedOptions[0]?.textContent).toBe("web-data (local)");
      expect(view.queryByLabelText("Name of the new volume")).toBeNull();
    });

    it("sends the volume unchanged when saved before the list arrives", async () => {
      stubEventSource();
      const { calls } = stubFetch({
        ...lists,
        "GET /volumes": () => new Promise<Response>(() => {}),
        "GET /containers/web": () => json(200, { ...info, spec: withVolume }),
        "GET /images/present": () => json(200, { present: true }),
        "PUT /containers/aaaaaaaaaaaaaaaa": () => json(200, { id: "aaaaaaaaaaaaaaaa", recreated: false }),
      });
      const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
      await view.findByLabelText("Volume name");
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/aaaaaaaaaaaaaaaa"));
      const sent = calls.find((c) => c.method === "PUT")?.body as Spec;
      expect(sent.mounts).toEqual([{ type: "volume", source: "web-data", target: "/data", readOnly: false }]);
    });
  });

  it("pulls the image again when asked, although it is present", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /containers/web": () => json(200, { ...info, spec }),
      "GET /images/present": () => json(200, { present: true }),
      "POST /pulls": () => json(202, { id: "p3", ref: "nginx:1.29" }),
      "PUT /containers/aaaaaaaaaaaaaaaa": () => json(200, { id: "aaaaaaaaaaaaaaaa", recreated: false }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    fireEvent.click(await view.findByLabelText("Pull the image again before saving"));
    fireEvent.click(view.getByRole("button", { name: "Save" }));
    expect(await view.findByRole("dialog", { name: "Pulling nginx:1.29" })).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    sources.instances
      .find((s) => s.url.endsWith("/pulls/p3/events"))
      ?.emit("done", JSON.stringify({ ref: "nginx:1.29" }));
    await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/aaaaaaaaaaaaaaaa"));
    expect(calls.some((c) => c.path.startsWith("/images/present"))).toBe(false);
    expect(view.toasts.shown.map((s) => s.message)).toContain("web was saved.");
  });

  it("shows a failed pull and stays on the form", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      ...lists,
      "GET /containers/web": () => json(200, { ...info, spec }),
      "GET /images/present": () => json(200, { present: false }),
      "POST /pulls": () => json(202, { id: "p4", ref: "nginx:1.29" }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    fireEvent.click(await view.findByRole("button", { name: "Save" }));
    await view.findByRole("dialog", { name: "Pulling nginx:1.29" });
    sources.instances
      .find((s) => s.url.endsWith("/pulls/p4/events"))
      ?.emit("error", JSON.stringify({ message: "pull access denied" }));
    expect(await view.findByText("The pull failed: pull access denied")).toBeTruthy();
    expect(view.queryByRole("dialog")).toBeNull();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    expect((view.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows the stored network, restart policy and retries", async () => {
    stubEventSource();
    stubFetch({
      ...lists,
      "GET /containers/web": () =>
        json(200, {
          ...info,
          spec: {
            ...spec,
            network: "front",
            privileged: true,
            restart: { policy: "on-failure", maxRetries: 3 },
          },
        }),
    });
    const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
    await view.findByLabelText("Name");
    await waitFor(() => expect((view.getByLabelText("Network") as HTMLSelectElement).value).toBe("front"));
    expect((view.getByLabelText("Restart policy") as HTMLSelectElement).value).toBe("on-failure");
    expect((view.getByLabelText("Maximum retries") as HTMLInputElement).value).toBe("3");
    expect((view.getByLabelText("Privileged") as HTMLInputElement).checked).toBe(true);
    const warning = view.getByText("A privileged container has full access to the host.");
    expect(warning.classList.contains("text-warning")).toBe(true);
    // the restart policy is a section of its own, apart from the network
    const restart = view.getByRole("group", { name: "Restart policy" });
    expect(restart.contains(view.getByLabelText("Restart policy"))).toBe(true);
    expect(restart.contains(view.getByLabelText("Maximum retries"))).toBe(true);
    expect(restart.contains(view.getByLabelText("Network"))).toBe(false);
  });

  describe("limits", () => {
    const limitsOf = (resources: Spec["resources"]): Spec => ({ ...spec, resources });
    async function edit(resources: Spec["resources"], routes: Record<string, () => Response> = {}) {
      stubEventSource();
      const { calls } = stubFetch({
        ...lists,
        "GET /containers/web": () => json(200, { ...info, spec: limitsOf(resources) }),
        "GET /images/present": () => json(200, { present: true }),
        "PUT /containers/aaaaaaaaaaaaaaaa": () => json(200, { id: "aaaaaaaaaaaaaaaa", recreated: false }),
        ...routes,
      });
      const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
      await view.findByLabelText("Name");
      const memory = () => view.getByLabelText("Memory limit") as HTMLInputElement;
      const cpus = () => view.getByLabelText("CPU limit (cores)") as HTMLInputElement;
      const unit = () => view.getByLabelText("Unit") as HTMLSelectElement;
      const sent = () => calls.find((c) => c.method === "PUT")?.body as Spec;
      return { view, calls, memory, cpus, unit, sent };
    }
    const recreateHint = /These changes recreate the container/;

    it("shows stored limits as they are and saves an unchanged form without a recreation hint", async () => {
      stubEventSource();
      const stored: Spec = { ...info.spec, resources: { memory: 1536 * MiB, cpus: 0.3 } };
      stubFetch({ ...lists, "GET /containers/web": () => json(200, { ...info, spec: stored }) });
      const view = mount(() => <ContainerEdit params={{ id: "web" }} />, { path: "/containers/web/edit" });
      expect(((await view.findByLabelText("Memory limit")) as HTMLInputElement).value).toBe("1536");
      expect((view.getByLabelText("Unit") as HTMLSelectElement).value).toBe("MiB");
      expect((view.getByLabelText("CPU limit (cores)") as HTMLInputElement).value).toBe("0.3");
      expect(view.queryByText(/These changes recreate/)).toBeNull();
    });

    it.each<[string, Spec["resources"], string, string, string]>([
      ["a whole number of GiB", { memory: 2 * GiB, cpus: 0 }, "2", "GiB", ""],
      [
        "MiB for a number of MiB that is no whole GiB",
        { memory: 1536 * MiB, cpus: 0.25 },
        "1536",
        "MiB",
        "0.25",
      ],
      ["no limits", { memory: 0, cpus: 0 }, "", "MiB", ""],
      [
        "MiB with fractions for a limit set outside edelweiss",
        { memory: 500_000_000, cpus: 0 },
        "476.837158203125",
        "MiB",
        "",
      ],
    ])("shows %s", async (_what, resources, memoryText, unitText, cpusText) => {
      const { memory, unit, cpus } = await edit(resources);
      expect([memory().value, unit().value, cpus().value]).toEqual([memoryText, unitText, cpusText]);
    });

    // the backend takes a missing "resources" as "no limits": the form must send what it was given
    it.each<[string, Spec["resources"]]>([
      ["both limits", { memory: 1536 * MiB, cpus: 0.3 }],
      ["a whole number of GiB and cores", { memory: 2 * GiB, cpus: 2 }],
      ["a memory limit that is no whole MiB (set outside edelweiss)", { memory: 500_000_000, cpus: 1.5 }],
      ["a CPU limit alone", { memory: 0, cpus: 0.25 }],
      ["a memory limit alone", { memory: 64 * MiB, cpus: 0 }],
      ["no limits", { memory: 0, cpus: 0 }],
    ])("sends the resources unchanged when only the name changes: %s", async (_what, resources) => {
      const { view, sent } = await edit(resources);
      type(view.getByLabelText("Name"), "web2");
      await settle();
      expect(view.queryByText(recreateHint)).toBeNull();
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/aaaaaaaaaaaaaaaa"));
      expect(sent().name).toBe("web2");
      expect(sent().resources).toEqual(resources);
      expect(view.toasts.shown.map((s) => s.message)).toEqual(["web2 was saved."]);
    });

    it("changes a limit without a recreation hint, removing one asks for it", async () => {
      const { view, memory, cpus, unit } = await edit({ memory: 256 * MiB, cpus: 1 });
      type(memory(), "512");
      type(cpus(), "2");
      await settle();
      expect(view.queryByText(recreateHint)).toBeNull();
      type(memory(), "");
      expect(await view.findByText(recreateHint)).toBeTruthy();
      type(memory(), "512");
      await waitFor(() => expect(view.queryByText(recreateHint)).toBeNull());
      type(cpus(), "");
      expect(await view.findByText(recreateHint)).toBeTruthy();
      type(cpus(), "0.5");
      await waitFor(() => expect(view.queryByText(recreateHint)).toBeNull());
      // the unit changes the bytes, not the question whether there is a limit
      fireEvent.change(unit(), { target: { value: "GiB" } });
      await settle();
      expect(view.queryByText(recreateHint)).toBeNull();
    });

    it("does not call 0.1 + 0.2 cores a change from 0.3", async () => {
      const { view, cpus, sent } = await edit({ memory: 0, cpus: 0.3 });
      type(cpus(), String(0.1 + 0.2)); // "0.30000000000000004"
      await settle();
      expect(view.queryByText(recreateHint)).toBeNull();
      type(view.getByLabelText("Name"), "web2");
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/aaaaaaaaaaaaaaaa"));
      expect(sent().resources.cpus).toBe(0.1 + 0.2);
    });

    it("shows Docker's warnings as toasts before the success message, after a save", async () => {
      const { view } = await edit(
        { memory: 256 * MiB, cpus: 0 },
        {
          "PUT /containers/aaaaaaaaaaaaaaaa": () =>
            json(200, {
              id: "aaaaaaaaaaaaaaaa",
              recreated: false,
              warnings: ["Your kernel does not support swap limit capabilities", "second warning"],
            }),
        },
      );
      type(view.getByLabelText("Memory limit"), "512");
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      await waitFor(() => expect(view.history.get()).toBe("/x/docker/containers/aaaaaaaaaaaaaaaa"));
      expect(view.toasts.shown.map((s) => [s.kind, s.message])).toEqual([
        ["info", "Your kernel does not support swap limit capabilities"],
        ["info", "second warning"],
        ["success", "web was saved."],
      ]);
    });

    it("marks the limit the backend refused, and stays on the form", async () => {
      const message =
        "must not be more than 512 MiB, the limit of memory and swap together that was set outside edelweiss";
      const { view, memory, cpus } = await edit(
        { memory: 256 * MiB, cpus: 1 },
        {
          "PUT /containers/aaaaaaaaaaaaaaaa": () =>
            json(400, { error: { code: "invalid_container_spec", message, field: "resources.memory" } }),
        },
      );
      type(memory(), "1024");
      fireEvent.click(view.getByRole("button", { name: "Save" }));
      expect(await view.findByText(`Invalid input: ${message}`)).toBeTruthy();
      expect(memory().getAttribute("aria-invalid")).toBe("true");
      expect(cpus().getAttribute("aria-invalid")).toBe("false");
      expect(view.history.get()).toBe("/x/docker/containers/web/edit");
      expect(view.toasts.shown).toEqual([]);
    });
  });
});
