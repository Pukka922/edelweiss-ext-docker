import { fakeSession, sessionInfo } from "@edelweiss/sdk/testing";
import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Registry } from "../api";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { RegistryList } from "./RegistryList";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const ghcr: Registry = { id: "r1", name: "GHCR", address: "ghcr.io", username: "octo", hasPassword: true };
const local: Registry = { id: "r2", name: "Pi", address: "pi.local:5000", hasPassword: false };

const messages = (view: ReturnType<typeof mount>) => view.toasts.shown.map((s) => s.message);
const type = (el: HTMLElement, value: string) => fireEvent.input(el, { target: { value } });

describe("RegistryList", () => {
  it("is read-only without admin mode and offers it", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /registries": () => json(200, { registries: [ghcr, local], writable: false }),
    });
    const session = fakeSession({ canAdmin: true });
    const view = mount(RegistryList, { path: "/registries", session });
    expect(await view.findByText("GHCR")).toBeTruthy();
    expect(view.getByText("octo")).toBeTruthy();
    expect(view.getByText("without login")).toBeTruthy();
    expect(view.getByText("Registries can only be changed in admin mode.")).toBeTruthy();
    expect(view.queryByRole("button", { name: "Add registry" })).toBeNull();
    expect(view.queryByRole("button", { name: "Edit GHCR" })).toBeNull();
    fireEvent.click(view.getByRole("button", { name: "Admin mode" }));
    expect(session.calls.requestAdmin).toBe(1);
    session.setInfo(sessionInfo({ admin: true }));
    await waitFor(() => expect(calls.filter((c) => c.path === "/registries")).toHaveLength(2));
  });

  it("tests a stored entry", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /registries": () => json(200, { registries: [ghcr], writable: false }),
      "POST /registries/test": () =>
        json(200, { ok: false, message: "unauthorized: incorrect username or password" }),
    });
    const view = mount(RegistryList, { path: "/registries" });
    await view.findByText("GHCR");
    fireEvent.click(view.getByRole("button", { name: "Test connection GHCR" }));
    await waitFor(() =>
      expect(messages(view)).toContain("Connection failed: unauthorized: incorrect username or password"),
    );
    expect(calls.find((c) => c.path === "/registries/test")?.body).toEqual({ id: "r1" });
  });

  it("adds a registry with a login, after testing it", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /registries": () => json(200, { registries: [], writable: true }),
      "POST /registries/test": () => json(200, { ok: true, message: "" }),
      "POST /registries": () =>
        json(201, { id: "r3", name: "Pi", address: "pi.local:5000", username: "myUser", hasPassword: true }),
    });
    const view = mount(RegistryList, { path: "/registries" });
    fireEvent.click(await view.findByRole("button", { name: "Add registry" }));
    await view.findByRole("dialog", { name: "Add registry" });
    type(view.getByLabelText("Name"), "Pi");
    type(view.getByLabelText("Address"), "pi.local:5000");
    fireEvent.click(view.getByLabelText("with login"));
    type(view.getByLabelText("User name"), "myUser");
    type(view.getByLabelText("Password"), "pi-pw");
    fireEvent.click(view.getByRole("button", { name: "Test connection" }));
    expect(await view.findByText("Connection works.")).toBeTruthy();
    expect(calls.find((c) => c.path === "/registries/test")?.body).toEqual({
      id: "",
      address: "pi.local:5000",
      auth: true,
      username: "myUser",
      password: "pi-pw",
    });
    fireEvent.click(view.getByRole("button", { name: "Add" }));
    await waitFor(() => expect(messages(view)).toContain("Pi was added."));
    expect(calls.find((c) => c.method === "POST" && c.path === "/registries")?.body).toEqual({
      name: "Pi",
      address: "pi.local:5000",
      auth: true,
      username: "myUser",
      password: "pi-pw",
    });
    expect(view.queryByRole("dialog")).toBeNull();
    expect(calls.filter((c) => c.method === "GET" && c.path === "/registries")).toHaveLength(2);
  });

  it("keeps the password when editing and marks the field the backend refused", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /registries": () => json(200, { registries: [ghcr], writable: true }),
      "PUT /registries/r1": () =>
        json(400, {
          error: { code: "invalid_registry", message: "address has an invalid port", field: "address" },
        }),
    });
    const view = mount(RegistryList, { path: "/registries" });
    fireEvent.click(await view.findByRole("button", { name: "Edit GHCR" }));
    await view.findByRole("dialog", { name: "Edit registry" });
    expect((view.getByLabelText("Name") as HTMLInputElement).value).toBe("GHCR");
    expect((view.getByLabelText("with login") as HTMLInputElement).checked).toBe(true);
    expect((view.getByLabelText("User name") as HTMLInputElement).value).toBe("octo");
    const password = view.getByLabelText("Password") as HTMLInputElement;
    expect(password.value).toBe("");
    expect(password.placeholder).toBe("leave empty to keep it");
    type(view.getByLabelText("Address"), "http://ghcr.io:99999");
    expect(view.getByText(/also needs insecure-registries/)).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "Save" }));
    expect(await view.findByText("Invalid input: address has an invalid port")).toBeTruthy();
    expect(view.getByLabelText("Address").getAttribute("aria-invalid")).toBe("true");
    expect(view.getByLabelText("Name").getAttribute("aria-invalid")).not.toBe("true");
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ password: "" });
  });

  it("deletes an entry", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /registries": () => json(200, { registries: [ghcr], writable: true }),
      "DELETE /registries/r1": () => new Response(null, { status: 204 }),
    });
    const view = mount(RegistryList, { path: "/registries" });
    fireEvent.click(await view.findByRole("button", { name: "Delete GHCR" }));
    const dialog = await view.findByRole("dialog", { name: "Delete registry" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(messages(view)).toContain("GHCR was deleted."));
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("explains a list it may not read", async () => {
    stubEventSource();
    stubFetch({
      "GET /registries": () => json(403, { error: { code: "registries_forbidden", message: "x" } }),
    });
    const view = mount(RegistryList, { path: "/registries" });
    expect(
      await view.findByText(
        "The registry list is readable only in admin mode or for members of the group docker.",
      ),
    ).toBeTruthy();
  });

  describe("the form dialog", () => {
    const open = async () => {
      stubEventSource();
      const { calls } = stubFetch({
        "GET /registries": () => json(200, { registries: [], writable: true }),
        "POST /registries": () => json(201, { id: "r9", name: "X", address: "x.io", hasPassword: false }),
      });
      const view = mount(RegistryList, { path: "/registries" });
      fireEvent.click(await view.findByRole("button", { name: "Add registry" }));
      await view.findByRole("dialog", { name: "Add registry" });
      return { view, calls };
    };

    it("focuses its first field and closes with Escape, Cancel and the backdrop, saving nothing", async () => {
      const { view, calls } = await open();
      expect(document.activeElement).toBe(view.getByLabelText("Name"));
      fireEvent.keyDown(document, { key: "Escape" });
      await settle();
      expect(view.queryByRole("dialog")).toBeNull();

      fireEvent.click(view.getByRole("button", { name: "Add registry" }));
      await view.findByRole("dialog", { name: "Add registry" });
      fireEvent.click(view.getByRole("button", { name: "Cancel" }));
      await settle();
      expect(view.queryByRole("dialog")).toBeNull();

      fireEvent.click(view.getByRole("button", { name: "Add registry" }));
      await view.findByRole("dialog", { name: "Add registry" });
      fireEvent.click(document.querySelector(".modal-backdrop")!);
      await settle();
      expect(view.queryByRole("dialog")).toBeNull();
      expect(calls.filter((c) => c.method !== "GET")).toEqual([]);
      expect(messages(view)).toEqual([]);
    });

    it("does not submit twice while it is saving, and stays open after a failure with the values", async () => {
      stubEventSource();
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      const { calls } = stubFetch({
        "GET /registries": () => json(200, { registries: [], writable: true }),
        "POST /registries": async () => {
          await gate;
          return json(409, { error: { code: "registry_exists", message: "exists" } });
        },
      });
      const view = mount(RegistryList, { path: "/registries" });
      fireEvent.click(await view.findByRole("button", { name: "Add registry" }));
      await view.findByRole("dialog", { name: "Add registry" });
      type(view.getByLabelText("Name"), "Dup");
      type(view.getByLabelText("Address"), "ghcr.io");
      const form = view.getByRole("dialog").querySelector("form")!;
      fireEvent.submit(form);
      fireEvent.submit(form);
      await settle();
      expect((view.getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(true);
      // the dialog stays until the request ends
      fireEvent.keyDown(document, { key: "Escape" });
      fireEvent.click(view.getByRole("button", { name: "Cancel" }));
      await settle();
      expect(view.getByRole("dialog", { name: "Add registry" })).toBeTruthy();
      release();
      expect(await view.findByText("A registry with this address already exists.")).toBeTruthy();
      expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
      expect((view.getByRole("button", { name: "Add" }) as HTMLButtonElement).disabled).toBe(false);
      expect((view.getByLabelText("Name") as HTMLInputElement).value).toBe("Dup");
    });
  });

  describe("the test button of the dialog", () => {
    const open = async (testAnswer?: () => Response | Promise<Response>) => {
      stubEventSource();
      const { calls } = stubFetch({
        "GET /registries": () => json(200, { registries: [], writable: true }),
        "POST /registries/test": testAnswer ?? (() => json(200, { ok: true, message: "" })),
      });
      const view = mount(RegistryList, { path: "/registries" });
      fireEvent.click(await view.findByRole("button", { name: "Add registry" }));
      await view.findByRole("dialog", { name: "Add registry" });
      const button = () => view.getByRole("button", { name: "Test connection" }) as HTMLButtonElement;
      return { view, calls, button };
    };
    /** A test the spec finishes by hand. */
    const gated = () => {
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      return {
        release,
        answer: async () => {
          await gate;
          return json(200, { ok: true, message: "" });
        },
      };
    };

    it("needs an address", async () => {
      const { view, button } = await open();
      expect(button().disabled).toBe(true);
      type(view.getByLabelText("Address"), "pi.local:5000");
      expect(button().disabled).toBe(false);
      type(view.getByLabelText("Address"), "   ");
      expect(button().disabled).toBe(true);
    });

    it("is disabled while a test runs", async () => {
      const slow = gated();
      const { view, calls, button } = await open(slow.answer);
      type(view.getByLabelText("Address"), "pi.local:5000");
      fireEvent.click(button());
      await waitFor(() => expect(button().disabled).toBe(true));
      fireEvent.click(button());
      slow.release();
      expect(await view.findByText("Connection works.")).toBeTruthy();
      expect(button().disabled).toBe(false);
      expect(calls.filter((c) => c.path === "/registries/test")).toHaveLength(1);
    });

    it("removes the result as soon as a field changes", async () => {
      const { view, button } = await open(() => json(200, { ok: false, message: "unauthorized" }));
      type(view.getByLabelText("Address"), "pi.local:5000");
      fireEvent.click(button());
      expect(await view.findByText("Connection failed: unauthorized")).toBeTruthy();
      type(view.getByLabelText("Name"), "Pi");
      expect(view.queryByText(/Connection failed/)).toBeNull();

      fireEvent.click(button());
      await view.findByText("Connection failed: unauthorized");
      type(view.getByLabelText("Address"), "pi.local:5001");
      expect(view.queryByText(/Connection failed/)).toBeNull();

      fireEvent.click(button());
      await view.findByText("Connection failed: unauthorized");
      fireEvent.click(view.getByLabelText("with login"));
      expect(view.queryByText(/Connection failed/)).toBeNull();

      fireEvent.click(button());
      await view.findByText("Connection failed: unauthorized");
      type(view.getByLabelText("Password"), "x");
      expect(view.queryByText(/Connection failed/)).toBeNull();
    });

    it("drops the result of a test whose values changed while it was running", async () => {
      const slow = gated();
      const { view, button } = await open(slow.answer);
      type(view.getByLabelText("Address"), "pi.local:5000");
      fireEvent.click(button());
      await waitFor(() => expect(button().disabled).toBe(true));
      type(view.getByLabelText("Address"), "other.io");
      slow.release();
      await waitFor(() => expect(button().disabled).toBe(false));
      expect(view.queryByText("Connection works.")).toBeNull();
    });
  });
});
