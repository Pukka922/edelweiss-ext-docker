import { cleanup, fireEvent, render } from "@solidjs/testing-library";
import { IconTrash } from "@tabler/icons-solidjs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { IconButton } from "./IconButton";

afterEach(cleanup);

describe("IconButton", () => {
  it("is a button named by its label, with the tooltip and an icon, and clicks", () => {
    const onClick = vi.fn();
    const view = render(() => (
      <IconButton icon={IconTrash} label="Delete web" tooltip="Delete" danger onClick={onClick} />
    ));
    const button = view.getByRole("button", { name: "Delete web" });
    expect(button.getAttribute("type")).toBe("button");
    expect(button.classList.contains("text-error")).toBe(true);
    expect(button.querySelector("svg")).not.toBeNull();
    expect(button.closest("[data-tip]")?.getAttribute("data-tip")).toBe("Delete");
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledOnce();
  });

  it("hides the icon from assistive technology, so the label alone names the button", () => {
    const view = render(() => (
      <IconButton icon={IconTrash} label="Delete web" tooltip="Delete" onClick={() => {}} />
    ));
    const button = view.getByRole("button", { name: "Delete web" });
    expect(button.querySelector("svg")?.getAttribute("aria-hidden")).toBe("true");
    expect(button.textContent?.trim()).toBe("");
  });

  it("opens a one-line tooltip to the left, so it cannot make the box of a table scroll", () => {
    // daisyUI lays an invisible tooltip out all the time, and an absolutely placed box counts toward the scrolling area
    // of the overflow-x-auto box around a table: centered above a button in the last column it gave the box a
    // horizontal scrollbar, and a tooltip that wraps to two lines (the reason of a disabled delete) hangs below the
    // last row and gave it a vertical one. jsdom has no layout and does not load the stylesheet, so this test can only
    // pin the classes that fix it; the measurement was made in a browser with the built style.css.
    const view = render(() => (
      <IconButton icon={IconTrash} label="Delete web" tooltip="Delete" onClick={() => {}} />
    ));
    const tip = view.getByRole("button", { name: "Delete web" }).closest("[data-tip]");
    expect(tip?.classList.contains("tooltip")).toBe(true);
    expect(tip?.classList.contains("tooltip-left")).toBe(true);
    expect(tip?.classList.contains("before:max-w-none")).toBe(true);
    expect(tip?.classList.contains("before:whitespace-nowrap")).toBe(true);
  });

  it("does nothing while disabled", () => {
    const onClick = vi.fn();
    const view = render(() => (
      <IconButton icon={IconTrash} label="Delete web" tooltip="Delete" disabled onClick={onClick} />
    ));
    const button = view.getByRole("button", { name: "Delete web" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
  });

  it("is red only while a danger button can be clicked", () => {
    const enabled = render(() => (
      <IconButton icon={IconTrash} label="Delete web" tooltip="Delete" danger onClick={() => {}} />
    ));
    expect(enabled.getByRole("button", { name: "Delete web" }).classList.contains("text-error")).toBe(true);
    cleanup();
    const disabled = render(() => (
      <IconButton icon={IconTrash} label="Delete web" tooltip="Delete" danger disabled onClick={() => {}} />
    ));
    const button = disabled.getByRole("button", { name: "Delete web" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    expect(button.classList.contains("text-error")).toBe(false);
  });
});
