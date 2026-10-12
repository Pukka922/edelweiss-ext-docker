import { createI18n, type Locale } from "@edelweiss/sdk";
import { FakeEventSource, fakeSession, recordingToasts, TestShell } from "@edelweiss/sdk/testing";
import { createMemoryHistory, MemoryRouter, Route } from "@solidjs/router";
import { render } from "@solidjs/testing-library";
import type { Component } from "solid-js";
import { vi } from "vitest";
import de from "../../public/locales/de.json";
import en from "../../public/locales/en.json";

export interface FakeRequest {
  method: string;
  path: string; // below /ext/docker/api, with the query
  body: unknown;
}

export type Handler = (req: FakeRequest) => Response | Promise<Response>;

export function json(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Stubs fetch: routes are "METHOD /path" below the API base (with the query, or without it as a fallback). */
export function stubFetch(routes: Record<string, Handler>) {
  const calls: FakeRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
      const path = String(input).replace(/^\/ext\/docker\/api/, "");
      const req: FakeRequest = {
        method: init.method ?? "GET",
        path,
        body: typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      };
      calls.push(req);
      const handler = routes[`${req.method} ${path}`] ?? routes[`${req.method} ${path.split("?")[0]}`];
      return handler ? handler(req) : json(404, { error: { code: "not_found", message: path } });
    }),
  );
  return { calls };
}

/**
 * Replaces EventSource by the SDK's fake; FakeEventSource.instances lists the streams opened. A test fires the open
 * of a stream (the first connection and every reconnect of a real one) with instances[i].open(), its events with
 * emit(name, json) and a failure with fail(closed).
 */
export function stubEventSource() {
  FakeEventSource.instances = [];
  vi.stubGlobal("EventSource", FakeEventSource);
  return FakeEventSource;
}

/** Waits for pending fetches, promise callbacks and timers shorter than ms. */
export async function settle(ms = 0) {
  for (let i = 0; i < 5; i++) await new Promise((resolve) => setTimeout(resolve, 0));
  if (ms > 0) await new Promise((resolve) => setTimeout(resolve, ms));
}

/** Renders a page of the extension at path (inside the extension, e.g. "/containers") with the shell's contexts. */
export function mount(
  page: Component,
  opts: { path?: string; locale?: Locale; session?: ReturnType<typeof fakeSession> } = {},
) {
  const i18n = createI18n(opts.locale ?? "en");
  i18n.register("docker", "en", en);
  i18n.register("docker", "de", de);
  const session = opts.session ?? fakeSession();
  const toasts = recordingToasts();
  const history = createMemoryHistory();
  history.set({ value: `/x/docker${opts.path ?? "/"}` });
  const view = render(() => (
    <TestShell extension="docker" i18n={i18n} session={session.api} toasts={toasts}>
      <MemoryRouter history={history}>
        <Route path="/x/docker/*rest" component={page} />
      </MemoryRouter>
    </TestShell>
  ));
  return { ...view, i18n, session, toasts, history };
}
