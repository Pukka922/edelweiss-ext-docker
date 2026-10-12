import { cleanup, render } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, describe, expect, it } from "vitest";
import { Input, Select } from "./Field";

afterEach(cleanup);

describe("Input and Select", () => {
  it("add the error class and aria-invalid for an invalid field, and only then", () => {
    const view = render(() => (
      <>
        <Input aria-label="a" class="input-sm w-28" invalid />
        <Input aria-label="b" class="input-sm" invalid={false} />
        <Input aria-label="c" />
        <Select aria-label="d" invalid>
          <option value="x">x</option>
        </Select>
        <Select aria-label="e">
          <option value="y">y</option>
        </Select>
      </>
    ));
    const a = view.getByLabelText("a");
    expect(a.className).toBe("input input-sm w-28 input-error");
    expect(a.getAttribute("aria-invalid")).toBe("true");
    const b = view.getByLabelText("b");
    expect(b.className).toBe("input input-sm");
    expect(b.getAttribute("aria-invalid")).toBe("false");
    expect(view.getByLabelText("c").classList.contains("input-error")).toBe(false);
    const d = view.getByLabelText("d");
    expect(d.className).toBe("select select-error");
    expect(d.getAttribute("aria-invalid")).toBe("true");
    expect(d.querySelectorAll("option")).toHaveLength(1);
    expect(view.getByLabelText("e").className).toBe("select");
  });

  it("follow a change of invalid and pass the other attributes and handlers on", () => {
    const [bad, setBad] = createSignal(false);
    const [text, setText] = createSignal("");
    const view = render(() => (
      <Input
        aria-label="f"
        required
        placeholder="p"
        invalid={bad()}
        value={text()}
        onInput={(e) => setText(e.currentTarget.value)}
      />
    ));
    const field = view.getByLabelText("f") as HTMLInputElement;
    expect(field.required).toBe(true);
    expect(field.placeholder).toBe("p");
    expect(field.classList.contains("input-error")).toBe(false);
    setBad(true);
    expect(field.classList.contains("input-error")).toBe(true);
    expect(field.getAttribute("aria-invalid")).toBe("true");
    field.value = "x";
    field.dispatchEvent(new InputEvent("input", { bubbles: true }));
    expect(text()).toBe("x");
    setBad(false);
    expect(field.classList.contains("input-error")).toBe(false);
  });
});
