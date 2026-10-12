import { type ExtensionRoute, useApi, useI18n } from "@edelweiss/sdk";
import { fakeSession } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent } from "@solidjs/testing-library";
import { createResource, Show } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { App, routes } from "./App";
import type { NetworkDetail } from "./api";
import { errorText } from "./errors";
import { json, mount, settle, stubEventSource, stubFetch } from "./test/mount";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const ok = { docker: "ok", version: "29.8.2", apiVersion: "1.55", admin: false };

describe("App", () => {
  it("shows the tabs and the daemon version when Docker is reachable", async () => {
    stubEventSource();
    stubFetch({ "GET /status": () => json(200, ok) });
    const view = mount(App, { path: "/" });
    expect(await view.findByText("Docker 29.8.2")).toBeTruthy();
    for (const tab of ["Containers", "Images", "Volumes", "Networks", "Registries"]) {
      expect(view.getByRole("tab", { name: tab })).toBeTruthy();
    }
    fireEvent.click(view.getByRole("tab", { name: "Images" }));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/images");
  });

  it("opens the page of each tab", async () => {
    stubEventSource();
    stubFetch({
      "GET /status": () => json(200, ok),
      "GET /containers": () => json(200, []),
      "GET /images": () => json(200, []),
      "GET /volumes": () => json(200, []),
      "GET /networks": () => json(200, []),
      "GET /registries": () => json(200, { registries: [], writable: false }),
    });
    const view = mount(App, { path: "/" });
    expect(await view.findByText("There are no containers yet.")).toBeTruthy();
    const pages: [string, string][] = [
      ["Images", "There are no images."],
      ["Volumes", "There are no volumes."],
      ["Networks", "There are no networks."],
      ["Registries", "Registries can only be changed in admin mode."],
    ];
    for (const [tab, text] of pages) {
      fireEvent.click(view.getByRole("tab", { name: tab }));
      expect(await view.findByText(text)).toBeTruthy();
    }
  });

  it("opens the detail of a network below the Networks tab", async () => {
    stubEventSource();
    const detail: NetworkDetail = {
      id: "f",
      name: "front",
      driver: "bridge",
      scope: "local",
      internal: false,
      predefined: false,
      containers: 0,
      options: {},
      labels: {},
      attached: [],
    };
    stubFetch({
      "GET /status": () => json(200, ok),
      "GET /networks/f": () => json(200, detail),
      "GET /containers": () => json(200, []),
    });
    const view = mount(App, { path: "/networks/f" });
    expect(await view.findByRole("heading", { name: "front" })).toBeTruthy();
    expect(view.getByText("No container is connected.")).toBeTruthy();
    expect(view.getByRole("tab", { name: "Networks" }).getAttribute("aria-selected")).toBe("true");
  });

  it("says how to start Docker when it is not running, and checks again on focus", async () => {
    let answer: unknown = {
      docker: "unavailable",
      admin: false,
      message: "dial unix /var/run/docker.sock: no such file",
    };
    stubEventSource();
    const { calls } = stubFetch({ "GET /status": () => json(200, answer) });
    const view = mount(App);
    expect(await view.findByText("Docker is not running")).toBeTruthy();
    expect(view.getByText("rc-service docker start")).toBeTruthy();
    answer = ok;
    window.dispatchEvent(new Event("focus"));
    expect(await view.findByRole("tab", { name: "Containers" })).toBeTruthy();
    expect(calls.filter((c) => c.path === "/status")).toHaveLength(2);
  });

  it("offers admin mode and the group docker when access is refused", async () => {
    stubEventSource();
    stubFetch({
      "GET /status": () => json(200, { docker: "forbidden", admin: false, message: "permission denied" }),
    });
    const session = fakeSession({ canAdmin: true, user: { name: "anna", uid: 1000, groups: ["anna"] } });
    const view = mount(App, { session });
    expect(await view.findByText("No access to Docker")).toBeTruthy();
    expect(view.getByText("addgroup anna docker")).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Admin mode" }));
    await settle();
    expect(session.calls.requestAdmin).toBe(1);
  });

  it("does not offer admin mode to a user who cannot use it", async () => {
    stubEventSource();
    stubFetch({ "GET /status": () => json(200, { docker: "forbidden", admin: false }) });
    const view = mount(App, { session: fakeSession({ canAdmin: false }) });
    await view.findByText("No access to Docker");
    expect(view.queryByRole("button", { name: "Admin mode" })).toBeNull();
  });

  it("checks Docker again when admin mode changes", async () => {
    stubEventSource();
    const { calls } = stubFetch({ "GET /status": () => json(200, ok) });
    const session = fakeSession({ admin: false });
    const view = mount(App, { session });
    await view.findByText("Docker 29.8.2");
    expect(calls.filter((c) => c.path === "/status")).toHaveLength(1);
    session.setInfo((info) => (info ? { ...info, admin: true } : info));
    await settle();
    expect(calls.filter((c) => c.path === "/status")).toHaveLength(2);
  });

  it("replaces a page by the hint when one of its requests finds Docker gone", async () => {
    // a page like the real ones: it loads data and shows the error of a failed request through errorText
    function Probe() {
      const api = useApi();
      const { t } = useI18n();
      const [containers] = createResource(() => api.get<unknown>("/containers"));
      return <Show when={containers.error}>{(err) => <p>{errorText(err(), t)}</p>}</Show>;
    }
    const probe: ExtensionRoute = { path: "/probe", component: Probe };
    routes.push(probe);
    try {
      let checks = 0;
      stubEventSource();
      const { calls } = stubFetch({
        "GET /status": () =>
          json(
            200,
            ++checks === 1 ? ok : { docker: "unavailable", admin: false, message: "dial unix: no such file" },
          ),
        "GET /containers": () =>
          json(503, { error: { code: "docker_unavailable", message: "dial unix: no such file" } }),
      });
      const view = mount(App, { path: "/probe" });
      expect(await view.findByText("Docker is not running")).toBeTruthy();
      expect(view.getByText("rc-service docker start")).toBeTruthy();
      expect(view.queryByText("Docker is not running: dial unix: no such file")).toBeNull(); // the page is gone
      expect(view.queryByRole("tab", { name: "Containers" })).toBeNull();
      expect(calls.filter((c) => c.path === "/status")).toHaveLength(2);
    } finally {
      routes.splice(routes.indexOf(probe), 1);
    }
  });
});
