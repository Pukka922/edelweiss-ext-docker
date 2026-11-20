import { cleanup, fireEvent } from "@solidjs/testing-library";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount } from "../test/mount";
import { Pager } from "./Pager";

afterEach(cleanup);

describe("Pager", () => {
  it("is not shown for a single page", () => {
    const view = mount(() => <Pager page={1} pages={1} onPage={() => {}} />);
    expect(view.queryByRole("button")).toBeNull();
    expect(view.container.textContent).toBe("");
  });

  it("names the page and moves to the first, previous, next and last page", () => {
    const onPage = vi.fn();
    const view = mount(() => <Pager page={2} pages={4} onPage={onPage} />);
    expect(view.getByText("Page 2 of 4")).toBeTruthy();
    fireEvent.click(view.getByRole("button", { name: "First page" }));
    fireEvent.click(view.getByRole("button", { name: "Previous page" }));
    fireEvent.click(view.getByRole("button", { name: "Next page" }));
    fireEvent.click(view.getByRole("button", { name: "Last page" }));
    expect(onPage.mock.calls).toEqual([[1], [1], [3], [4]]);
  });

  it("disables the buttons at its ends", () => {
    const disabled = (view: ReturnType<typeof mount>, name: string) =>
      (view.getByRole("button", { name }) as HTMLButtonElement).disabled;
    const first = mount(() => <Pager page={1} pages={3} onPage={() => {}} />, { locale: "de" });
    expect(first.getByText("Seite 1 von 3")).toBeTruthy();
    expect(disabled(first, "Erste Seite")).toBe(true);
    expect(disabled(first, "Vorherige Seite")).toBe(true);
    expect(disabled(first, "Nächste Seite")).toBe(false);
    cleanup();
    const last = mount(() => <Pager page={3} pages={3} onPage={() => {}} />);
    expect(disabled(last, "Next page")).toBe(true);
    expect(disabled(last, "Last page")).toBe(true);
    expect(disabled(last, "Previous page")).toBe(false);
  });
});
