import { type JSX, splitProps } from "solid-js";

/**
 * Form controls that a validation error of the backend can name (see errorField in errors.ts). daisyUI draws the red
 * border from the class input-error / select-error, not from aria-invalid (that needs its validator class), so an
 * invalid field gets both: the class for the eye and aria-invalid for assistive technology. class takes the sizes and
 * the like; the base class (input, select) is added here.
 */
type Invalid = { invalid?: boolean };

const classes = (...names: (string | false | undefined)[]) => names.filter(Boolean).join(" ");

export function Input(props: JSX.InputHTMLAttributes<HTMLInputElement> & Invalid) {
  const [own, rest] = splitProps(props, ["invalid", "class"]);
  return (
    <input
      {...rest}
      class={classes("input", own.class, own.invalid && "input-error")}
      aria-invalid={own.invalid ? "true" : "false"}
    />
  );
}

export function Select(props: JSX.SelectHTMLAttributes<HTMLSelectElement> & Invalid) {
  const [own, rest] = splitProps(props, ["invalid", "class"]);
  return (
    <select
      {...rest}
      class={classes("select", own.class, own.invalid && "select-error")}
      aria-invalid={own.invalid ? "true" : "false"}
    />
  );
}
