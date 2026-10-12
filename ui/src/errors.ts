import { ApiError, type Translator } from "@edelweiss/sdk";
import { noteDockerError } from "./gate";

/**
 * A text for an error of the backend: the translation of its code (with Docker's message), else its message. Every
 * page shows its errors through it, so it also tells the App when Docker went away or refuses this user: the App then
 * replaces the page by the hint panel (the gate).
 */
export function errorText(err: unknown, t: Translator["t"]): string {
  noteDockerError(err);
  if (err instanceof ApiError) {
    const key = `errors.${err.code}`;
    const text = t(key, { message: err.message });
    return text === key ? err.message : text;
  }
  return err instanceof Error ? err.message : String(err);
}

/** The field an invalid_* error names (details.field), if any. */
export function errorField(err: unknown): string | undefined {
  if (err instanceof ApiError && typeof err.details.field === "string") return err.details.field;
  return undefined;
}
