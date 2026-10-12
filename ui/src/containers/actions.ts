import { type Translator, useApi, useI18n, useToast } from "@edelweiss/sdk";
import { createSignal } from "solid-js";
import type { PublishedPort } from "../api";
import { errorText } from "../errors";

type Target = { id: string; name: string };
type Action = "start" | "stop" | "restart";

const done: Record<Action, string> = {
  start: "containers.started",
  stop: "containers.stopped",
  restart: "containers.restarted",
};

/**
 * Start/stop/restart and delete, with toasts. busy(id) is true while an action on that container runs; the actions of
 * several containers can run at the same time, and each keeps its own container busy until its own request ends.
 */
export function useContainerActions() {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  // the IDs of the requests in flight (one entry per request)
  const [inFlight, setInFlight] = createSignal<string[]>([]);
  const busy = (id: string) => inFlight().includes(id);
  const begin = (id: string) => setInFlight((ids) => [...ids, id]);
  const end = (id: string) =>
    setInFlight((ids) => {
      const i = ids.indexOf(id);
      return i < 0 ? ids : [...ids.slice(0, i), ...ids.slice(i + 1)];
    });
  const run = async (c: Target, action: Action) => {
    begin(c.id);
    try {
      await api.post(`/containers/${encodeURIComponent(c.id)}/${action}`);
      toast.success(t(done[action], { name: c.name }));
    } catch (err) {
      toast.error(errorText(err, t));
    } finally {
      end(c.id);
    }
  };
  const remove = async (c: Target, withVolumes: boolean): Promise<boolean> => {
    begin(c.id);
    try {
      await api.del(`/containers/${encodeURIComponent(c.id)}${withVolumes ? "?volumes=1" : ""}`);
      toast.success(t("containers.deleted", { name: c.name }));
      return true;
    } catch (err) {
      toast.error(errorText(err, t));
      return false;
    } finally {
      end(c.id);
    }
  };
  return { busy, run, remove };
}

/**
 * Whether the container has a process to stop (Stop and Restart apply, not Start): it runs, restarts in a loop or is
 * paused. A restarting or paused container reports its own state, not "running".
 */
export function isActive(state: string): boolean {
  return state === "running" || state === "restarting" || state === "paused";
}

/** The daisyUI badge for a container state. */
export function stateBadge(state: string): string {
  switch (state) {
    case "running":
      return "badge badge-success badge-sm";
    case "restarting":
    case "paused":
      return "badge badge-warning badge-sm";
    case "created":
      return "badge badge-info badge-sm";
    default:
      return "badge badge-ghost badge-sm";
  }
}

/** "127.0.0.1:8080 → 80/tcp", or "80/tcp (not published)". */
export function portText(p: PublishedPort, t: Translator["t"]): string {
  const target = `${p.containerPort}/${p.protocol}`;
  if (!p.hostPort) return `${target} (${t("containers.notPublished")})`;
  return `${p.hostIp ? `${p.hostIp}:` : ""}${p.hostPort} → ${target}`;
}
