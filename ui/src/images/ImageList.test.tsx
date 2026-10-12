import { cleanup, fireEvent, waitFor } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ImageInfo } from "../api";
import { json, mount, settle, stubEventSource, stubFetch } from "../test/mount";
import { ImageList } from "./ImageList";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const nginx: ImageInfo = {
  id: `sha256:${"a".repeat(64)}`,
  tags: ["nginx:1.29", "nginx:latest"],
  size: 200 << 20,
  created: 1791633600,
  usedBy: [{ id: "c1", name: "web" }],
};
const old: ImageInfo = {
  id: `sha256:${"b".repeat(64)}`,
  tags: [],
  size: 50 << 20,
  created: 1791633000,
  usedBy: [],
};
const busybox: ImageInfo = {
  id: `sha256:${"c".repeat(64)}`,
  tags: ["busybox:1.37"],
  size: 4 << 20,
  created: 1791632000,
  usedBy: [],
};

const messages = (view: ReturnType<typeof mount>) => view.toasts.shown.map((s) => s.message);

describe("ImageList", () => {
  it("lists the images with their tags, size and users", async () => {
    stubEventSource();
    stubFetch({ "GET /images": () => json(200, [nginx, old, busybox]) });
    const view = mount(ImageList, { path: "/images" });
    expect(await view.findByText("nginx:1.29")).toBeTruthy();
    expect(view.getByText("nginx:latest")).toBeTruthy();
    expect(view.getByText("no tag")).toBeTruthy();
    expect(view.getByText("bbbbbbbbbbbb")).toBeTruthy();
    expect(view.getByText("200 MiB")).toBeTruthy();
    expect(view.getAllByText("unused")).toHaveLength(2);
    fireEvent.click(view.getByRole("button", { name: "web" }));
    await settle(); // the router navigates in a transition
    expect(view.history.get()).toBe("/x/docker/containers/c1");
  });

  it("filters to the unused images", async () => {
    stubEventSource();
    stubFetch({ "GET /images": () => json(200, [nginx, old, busybox]) });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("nginx:1.29");
    fireEvent.click(view.getByLabelText("Unused only"));
    expect(view.queryByText("nginx:1.29")).toBeNull();
    expect(view.getByText("busybox:1.37")).toBeTruthy();
  });

  it("deletes an unused image, but not one in use", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /images": () => json(200, [nginx, busybox]),
      [`DELETE /images/${encodeURIComponent(busybox.id)}`]: () => new Response(null, { status: 204 }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("nginx:1.29");
    expect((view.getByRole("button", { name: "Delete nginx:1.29" }) as HTMLButtonElement).disabled).toBe(
      true,
    );
    // the tooltip of a disabled button says why, the one of an enabled button names the action
    const tip = (name: string) =>
      view.getByRole("button", { name }).closest("[data-tip]")?.getAttribute("data-tip");
    expect(tip("Delete nginx:1.29")).toBe("The image is used by containers.");
    expect(tip("Delete busybox:1.37")).toBe("Delete");
    fireEvent.click(view.getByRole("button", { name: "Delete busybox:1.37" }));
    const dialog = await view.findByRole("dialog", { name: "Delete image" });
    expect(view.getByText("Delete busybox:1.37 with all its tags?")).toBeTruthy();
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(messages(view)).toContain("The image was deleted."));
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
    expect(calls.filter((c) => c.path === "/images")).toHaveLength(2);
  });

  it("removes the unused images after saying how many and how much", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /images": () => json(200, [nginx, old, busybox]),
      "POST /images/prune": () => json(200, { deleted: 2, spaceReclaimed: 54 << 20 }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("nginx:1.29");
    fireEvent.click(view.getByRole("button", { name: "Remove unused" }));
    const dialog = await view.findByRole("dialog", { name: "Remove unused images" });
    expect(view.getByText("Remove 2 unused images (54 MiB)?")).toBeTruthy();
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() => expect(messages(view)).toContain("2 images removed, 54 MiB freed."));
    expect(calls.some((c) => c.method === "POST" && c.path === "/images/prune")).toBe(true);
  });

  it("says when there is nothing to remove", async () => {
    stubEventSource();
    const { calls } = stubFetch({ "GET /images": () => json(200, [nginx]) });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("nginx:1.29");
    fireEvent.click(view.getByRole("button", { name: "Remove unused" }));
    await settle();
    expect(view.queryByRole("dialog")).toBeNull();
    expect(messages(view)).toContain("There are no unused images.");
    expect(calls.some((c) => c.path === "/images/prune")).toBe(false);
  });

  it("pulls an image from a stored registry with progress", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      "GET /images": () => json(200, [nginx]),
      "GET /registries": () =>
        json(200, {
          registries: [
            { id: "r1", name: "GHCR", address: "ghcr.io", username: "octo", hasPassword: true },
            { id: "r2", name: "Hub", address: "docker.io", username: "me", hasPassword: true },
          ],
          writable: false,
        }),
      "POST /pulls": () => json(202, { id: "p1", ref: "ghcr.io/owner/app:1" }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("nginx:1.29");
    fireEvent.click(view.getByRole("button", { name: "Pull image" }));
    await view.findByRole("dialog", { name: "Pull image" });
    await view.findByRole("option", { name: "GHCR (ghcr.io)" });
    // Docker Hub is the first choice; a stored Hub login is used for it anyway
    expect(view.queryByRole("option", { name: "Hub (docker.io)" })).toBeNull();
    fireEvent.change(view.getByLabelText("Registry"), { target: { value: "r1" } });
    fireEvent.input(view.getByLabelText("Image"), { target: { value: "owner/app" } });
    fireEvent.input(view.getByLabelText("Tag"), { target: { value: "1" } });
    fireEvent.click(view.getByRole("button", { name: "Pull" }));
    expect(await view.findByRole("dialog", { name: "Pulling ghcr.io/owner/app:1" })).toBeTruthy();
    expect(calls.find((c) => c.path === "/pulls")?.body).toEqual({
      registry: "r1",
      image: "owner/app",
      tag: "1",
    });
    sources.instances
      .find((s) => s.url.endsWith("/pulls/p1/events"))
      ?.emit("done", JSON.stringify({ ref: "ghcr.io/owner/app:1" }));
    await waitFor(() => expect(messages(view)).toContain("ghcr.io/owner/app:1 was pulled."));
    expect(view.queryByRole("dialog")).toBeNull();
    expect(calls.filter((c) => c.path === "/images")).toHaveLength(2);
  });

  it("shows a failed pull in the dialog", async () => {
    const sources = stubEventSource();
    stubFetch({
      "GET /images": () => json(200, []),
      "GET /registries": () => json(403, { error: { code: "registries_forbidden", message: "x" } }),
      "POST /pulls": () => json(202, { id: "p2", ref: "nosuch/image:latest" }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("There are no images.");
    fireEvent.click(view.getByRole("button", { name: "Pull image" }));
    await view.findByRole("dialog", { name: "Pull image" });
    // a list the user may not read is no news: Docker Hub alone, without a note
    await settle();
    expect(view.queryByText(/registry list/)).toBeNull();
    fireEvent.input(view.getByLabelText("Image"), { target: { value: "nosuch/image" } });
    fireEvent.click(view.getByRole("button", { name: "Pull" }));
    await view.findByRole("dialog", { name: "Pulling nosuch/image:latest" });
    sources.instances
      .find((s) => s.url.endsWith("/pulls/p2/events"))
      ?.emit("error", JSON.stringify({ message: "pull access denied" }));
    expect(await view.findByText("The pull failed: pull access denied")).toBeTruthy();
    expect(view.getByRole("dialog", { name: "Pull image" })).toBeTruthy();
  });

  it("names the image being pulled in the progress title", async () => {
    stubEventSource();
    stubFetch({
      "GET /images": () => json(200, []),
      "GET /registries": () => json(200, { registries: [], writable: false }),
      "POST /pulls": () => json(202, { id: "p3", ref: "docker.io/library/nginx:1.29" }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("There are no images.");
    fireEvent.click(view.getByRole("button", { name: "Pull image" }));
    await view.findByRole("dialog", { name: "Pull image" });
    fireEvent.input(view.getByLabelText("Image"), { target: { value: "library/nginx" } });
    fireEvent.input(view.getByLabelText("Tag"), { target: { value: "1.29" } });
    fireEvent.click(view.getByRole("button", { name: "Pull" }));
    expect(await view.findByRole("heading", { name: "Pulling docker.io/library/nginx:1.29" })).toBeTruthy();
  });

  it("marks the image field when the backend refuses the reference", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /images": () => json(200, []),
      "GET /registries": () => json(200, { registries: [], writable: false }),
      "POST /pulls": () =>
        json(400, {
          error: { code: "invalid_request", message: "image has an invalid name", field: "image" },
        }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("There are no images.");
    fireEvent.click(view.getByRole("button", { name: "Pull image" }));
    await view.findByRole("dialog", { name: "Pull image" });
    fireEvent.input(view.getByLabelText("Image"), { target: { value: " Bad Name " } });
    fireEvent.click(view.getByRole("button", { name: "Pull" }));
    expect(await view.findByText("Invalid input: image has an invalid name")).toBeTruthy();
    expect(view.getByLabelText("Image").getAttribute("aria-invalid")).toBe("true");
    expect(view.getByLabelText("Tag").getAttribute("aria-invalid")).not.toBe("true");
    // the image is trimmed; the empty tag is the backend's latest
    expect(calls.find((c) => c.path === "/pulls")?.body).toEqual({
      registry: "",
      image: "Bad Name",
      tag: "",
    });
  });

  it("lets the user leave the progress; the pull goes on and nothing is reported", async () => {
    const sources = stubEventSource();
    const { calls } = stubFetch({
      "GET /images": () => json(200, []),
      "GET /registries": () => json(200, { registries: [], writable: false }),
      "POST /pulls": () => json(202, { id: "p4", ref: "busybox:1.37" }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("There are no images.");
    fireEvent.click(view.getByRole("button", { name: "Pull image" }));
    await view.findByRole("dialog", { name: "Pull image" });
    fireEvent.input(view.getByLabelText("Image"), { target: { value: "busybox" } });
    fireEvent.click(view.getByRole("button", { name: "Pull" }));
    await view.findByRole("dialog", { name: "Pulling busybox:1.37" });
    fireEvent.click(view.getByRole("button", { name: "Close" }));
    await settle();
    expect(view.queryByRole("dialog")).toBeNull();
    expect(messages(view)).toEqual([]);
    expect(calls.filter((c) => c.path === "/images")).toHaveLength(1);
    // the stream of the pull is closed; the pull itself runs on in the backend
    expect(sources.instances.find((s) => s.url.endsWith("/pulls/p4/events"))?.readyState).toBe(
      sources.CLOSED,
    );
  });

  it("shows the message of a refused delete and loads the list again", async () => {
    stubEventSource();
    const { calls } = stubFetch({
      "GET /images": () => json(200, [busybox]),
      [`DELETE /images/${encodeURIComponent(busybox.id)}`]: () =>
        json(409, { error: { code: "image_in_use", message: "image is being used by a container" } }),
    });
    const view = mount(ImageList, { path: "/images" });
    await view.findByText("busybox:1.37");
    fireEvent.click(view.getByRole("button", { name: "Delete busybox:1.37" }));
    const dialog = await view.findByRole("dialog", { name: "Delete image" });
    fireEvent.click(dialog.querySelector("button.btn-error")!);
    await waitFor(() =>
      expect(view.toasts.shown).toContainEqual({
        kind: "error",
        message: "The image is used by containers.",
      }),
    );
    await waitFor(() => expect(calls.filter((c) => c.path === "/images")).toHaveLength(2));
  });

  describe("the pull form", () => {
    const empty = {
      "GET /images": () => json(200, []),
      "GET /registries": () => json(200, { registries: [], writable: false }),
    };

    it("focuses the image field; Escape closes it without a request and gives the focus back", async () => {
      stubEventSource();
      const { calls } = stubFetch(empty);
      const view = mount(ImageList, { path: "/images" });
      await view.findByText("There are no images.");
      const opener = view.getByRole("button", { name: "Pull image" });
      opener.focus();
      fireEvent.click(opener);
      await view.findByRole("dialog", { name: "Pull image" });
      // not the registry select above it: typing the name there would only search its options
      expect(document.activeElement).toBe(view.getByLabelText("Image"));
      fireEvent.keyDown(document, { key: "Escape" });
      await settle();
      expect(view.queryByRole("dialog")).toBeNull();
      expect(calls.some((c) => c.method === "POST")).toBe(false);
      expect(document.activeElement).toBe(opener);
    });

    it("cannot be cancelled while the pull is being started, so the pull never starts unseen", async () => {
      stubEventSource();
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      const { calls } = stubFetch({
        ...empty,
        "POST /pulls": async () => {
          await gate;
          return json(202, { id: "p5", ref: "busybox:1.37" });
        },
      });
      const view = mount(ImageList, { path: "/images" });
      await view.findByText("There are no images.");
      fireEvent.click(view.getByRole("button", { name: "Pull image" }));
      await view.findByRole("dialog", { name: "Pull image" });
      fireEvent.input(view.getByLabelText("Image"), { target: { value: "busybox" } });
      fireEvent.click(view.getByRole("button", { name: "Pull" }));
      await waitFor(() => expect(calls.some((c) => c.path === "/pulls")).toBe(true));
      fireEvent.keyDown(document, { key: "Escape" });
      fireEvent.click(view.getByRole("button", { name: "Cancel" }));
      await settle();
      expect(view.getByRole("dialog", { name: "Pull image" })).toBeTruthy();
      release();
      expect(await view.findByRole("dialog", { name: "Pulling busybox:1.37" })).toBeTruthy();
    });

    it("notes a registry list it cannot read and keeps Docker Hub selectable", async () => {
      stubEventSource();
      const { calls } = stubFetch({
        ...empty,
        "GET /registries": () =>
          json(500, { error: { code: "registries_corrupt", message: "database disk image is malformed" } }),
        "POST /pulls": () => json(202, { id: "p6", ref: "docker.io/library/busybox:latest" }),
      });
      const view = mount(ImageList, { path: "/images" });
      await view.findByText("There are no images.");
      fireEvent.click(view.getByRole("button", { name: "Pull image" }));
      await view.findByRole("dialog", { name: "Pull image" });
      expect(
        await view.findByText("The registry list cannot be read: database disk image is malformed"),
      ).toBeTruthy();
      expect(view.getByRole("option", { name: "Docker Hub" })).toBeTruthy();
      fireEvent.input(view.getByLabelText("Image"), { target: { value: "busybox" } });
      fireEvent.click(view.getByRole("button", { name: "Pull" }));
      await view.findByRole("dialog", { name: "Pulling docker.io/library/busybox:latest" });
      expect(calls.find((c) => c.path === "/pulls")?.body).toEqual({
        registry: "",
        image: "busybox",
        tag: "",
      });
    });

    it("starts with an empty tag that shows latest as its placeholder, and sends the tag typed", async () => {
      stubEventSource();
      const { calls } = stubFetch({
        ...empty,
        "POST /pulls": () => json(202, { id: "p7", ref: "busybox:1.37" }),
      });
      const view = mount(ImageList, { path: "/images" });
      await view.findByText("There are no images.");
      fireEvent.click(view.getByRole("button", { name: "Pull image" }));
      await view.findByRole("dialog", { name: "Pull image" });
      const tag = view.getByLabelText("Tag") as HTMLInputElement;
      expect(tag.value).toBe("");
      expect(tag.placeholder).toBe("latest");
      fireEvent.input(view.getByLabelText("Image"), { target: { value: "busybox" } });
      // typed into the empty field: no "latest" is left in front of it
      fireEvent.input(tag, { target: { value: "1.37" } });
      fireEvent.click(view.getByRole("button", { name: "Pull" }));
      await view.findByRole("dialog", { name: "Pulling busybox:1.37" });
      expect(calls.find((c) => c.path === "/pulls")?.body).toEqual({
        registry: "",
        image: "busybox",
        tag: "1.37",
      });
    });
  });
});
