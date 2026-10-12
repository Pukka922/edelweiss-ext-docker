import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Spec } from "../api";
import { info } from "../test/fixtures";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { ContainerCreate, ContainerEdit, needsRecreate } from "./ContainerForm";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const lists = {
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
    type(view.getByLabelText("Volume name"), "web-data");
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
      restart: { policy: "unless-stopped", maxRetries: 0 },
    });
    expect(calls.find((c) => c.path.startsWith("/images/present"))?.path).toBe(
      "/images/present?ref=nginx%3A1.29",
    );
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

  it("offers the named volumes, not the anonymous ones", async () => {
    stubEventSource();
    stubFetch(lists);
    const view = mount(ContainerCreate, { path: "/containers/new" });
    fireEvent.click(await view.findByRole("button", { name: "Add volume" }));
    await waitFor(() => expect(document.querySelectorAll("#docker-volume-names option")).toHaveLength(1));
    expect(document.querySelector("#docker-volume-names option")?.getAttribute("value")).toBe("web-data");
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
});

describe("ContainerEdit", () => {
  const spec: Spec = {
    name: "web",
    image: "nginx:1.29",
    command: "",
    network: "bridge",
    privileged: false,
    ports: [{ hostIp: "", hostPort: 8080, containerPort: 80, protocol: "tcp" }],
    mounts: [{ type: "volume", source: "a".repeat(64), target: "/cache", readOnly: false, anonymous: true }],
    env: [],
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
});
