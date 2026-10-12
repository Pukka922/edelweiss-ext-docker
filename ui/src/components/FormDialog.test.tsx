import { cleanup, fireEvent } from "@solidjs/testing-library";
import { createSignal, Show } from "solid-js";
import { afterEach, describe, expect, it, vi } from "vitest";
import { mount } from "../test/mount";
import { Input, Select } from "./Field";
import { Field, FormDialog } from "./FormDialog";

afterEach(cleanup);

const nothing = () => {};

describe("FormDialog", () => {
  describe("focus", () => {
    it("goes to the control marked autofocus", () => {
      const view = mount(() => (
        <FormDialog title="T" submitLabel="Go" busy={false} onSubmit={nothing} onCancel={nothing}>
          <Field label="Kind">
            <Select>
              <option value="a">a</option>
            </Select>
          </Field>
          <Field label="Name">
            <Input autofocus />
          </Field>
        </FormDialog>
      ));
      expect(document.activeElement).toBe(view.getByLabelText("Name"));
    });

    it("goes to the first input, select or textarea without one", () => {
      const view = mount(() => (
        <FormDialog title="T" submitLabel="Go" busy={false} onSubmit={nothing} onCancel={nothing}>
          <Field label="Notes">
            <textarea class="textarea" />
          </Field>
          <Field label="Name">
            <Input />
          </Field>
        </FormDialog>
      ));
      expect(document.activeElement).toBe(view.getByLabelText("Notes"));
    });

    const withOpener = () => {
      const [gone, setGone] = createSignal(false);
      const view = mount(() => {
        const [open, setOpen] = createSignal(false);
        return (
          <>
            <Show when={!gone()}>
              <button type="button" onClick={() => setOpen(true)}>
                Open
              </button>
            </Show>
            <Show when={open()}>
              <FormDialog
                title="T"
                submitLabel="Go"
                busy={false}
                onSubmit={nothing}
                onCancel={() => setOpen(false)}
              >
                <Field label="Name">
                  <Input />
                </Field>
              </FormDialog>
            </Show>
          </>
        );
      });
      const opener = view.getByRole("button", { name: "Open" });
      opener.focus();
      fireEvent.click(opener);
      return { view, opener, removeOpener: () => setGone(true) };
    };

    it("returns to the element that had it when the dialog opened", () => {
      const { view, opener } = withOpener();
      expect(document.activeElement).toBe(view.getByLabelText("Name"));
      fireEvent.keyDown(document, { key: "Escape" });
      expect(view.queryByRole("dialog")).toBeNull();
      expect(document.activeElement).toBe(opener);
    });

    it("does not return to an element that left the document", () => {
      const { view, opener, removeOpener } = withOpener();
      const focus = vi.spyOn(opener, "focus");
      removeOpener();
      expect(opener.isConnected).toBe(false);
      fireEvent.keyDown(document, { key: "Escape" });
      expect(view.queryByRole("dialog")).toBeNull();
      expect(focus).not.toHaveBeenCalled();
    });
  });

  it("ignores Escape, Cancel and the backdrop while busy, and cancels again afterwards", () => {
    const [busy, setBusy] = createSignal(true);
    const onCancel = vi.fn();
    const view = mount(() => (
      <FormDialog title="T" submitLabel="Go" busy={busy()} onSubmit={nothing} onCancel={onCancel}>
        <Field label="Name">
          <Input />
        </Field>
      </FormDialog>
    ));
    const cancelButton = view.getByRole("button", { name: "Cancel" }) as HTMLButtonElement;
    const backdrop = document.querySelector(".modal-backdrop") as HTMLElement;
    fireEvent.keyDown(document, { key: "Escape" });
    fireEvent.click(cancelButton);
    fireEvent.click(backdrop);
    expect(onCancel).not.toHaveBeenCalled();
    expect(cancelButton.disabled).toBe(true);

    setBusy(false);
    expect(cancelButton.disabled).toBe(false);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(onCancel).toHaveBeenCalledTimes(1);
    fireEvent.click(cancelButton);
    expect(onCancel).toHaveBeenCalledTimes(2);
    fireEvent.click(backdrop);
    expect(onCancel).toHaveBeenCalledTimes(3);
  });
});
