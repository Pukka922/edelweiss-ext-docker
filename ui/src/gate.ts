import { ApiError } from "@edelweiss/sdk";
import { createSignal } from "solid-js";

const [count, setCount] = createSignal(0);

/**
 * Changes whenever a page met an error that says Docker is gone or refuses this user (docker_unavailable,
 * docker_forbidden). The App watches it and asks /status again, so the hint panel replaces the page.
 */
export const dockerErrors = count;

/** Tells the App about an error of the backend; only the two Docker access codes count. */
export function noteDockerError(err: unknown): void {
  if (err instanceof ApiError && (err.code === "docker_unavailable" || err.code === "docker_forbidden")) {
    setCount((n) => n + 1);
  }
}
