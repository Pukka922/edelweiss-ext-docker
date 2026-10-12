import {
  ErrorAlert,
  type ExtensionRoute,
  ExtensionRouter,
  LoadingState,
  PageHeader,
  useApi,
  useExtension,
  useExtensionNavigate,
  useI18n,
  useSession,
} from "@edelweiss/sdk";
import { useLocation } from "@solidjs/router";
import { createEffect, createResource, For, Match, on, onCleanup, onMount, Switch } from "solid-js";
import { AccessPanel } from "./AccessPanel";
import type { Status } from "./api";
import { ContainerDetail } from "./containers/ContainerDetail";
import { ContainerCreate, ContainerEdit } from "./containers/ContainerForm";
import { ContainerList } from "./containers/ContainerList";
import { dockerErrors } from "./gate";
import { ImageList } from "./images/ImageList";
import { NetworkDetail } from "./networks/NetworkDetail";
import { NetworkList } from "./networks/NetworkList";
import { RegistryList } from "./registries/RegistryList";
import { VolumeList } from "./volumes/VolumeList";

/** The pages of the extension. */
export const routes: ExtensionRoute[] = [
  { path: "/", component: ContainerList },
  { path: "/containers", component: ContainerList },
  { path: "/containers/new", component: ContainerCreate },
  { path: "/containers/:id", component: ContainerDetail },
  { path: "/containers/:id/edit", component: ContainerEdit },
  { path: "/images", component: ImageList },
  { path: "/volumes", component: VolumeList },
  { path: "/networks", component: NetworkList },
  { path: "/networks/:id", component: NetworkDetail },
  { path: "/registries", component: RegistryList },
];

const tabs = [
  { path: "/containers", key: "tabs.containers" },
  { path: "/images", key: "tabs.images" },
  { path: "/volumes", key: "tabs.volumes" },
  { path: "/networks", key: "tabs.networks" },
  { path: "/registries", key: "tabs.registries" },
];

function Tabs() {
  const { t } = useI18n();
  const ext = useExtension();
  const location = useLocation();
  const navigate = useExtensionNavigate();
  // "/" is the container list
  const current = () => {
    const first = location.pathname.slice(ext.basePath.length).split("/")[1] ?? "";
    return first === "" ? "/containers" : `/${first}`;
  };
  return (
    <div role="tablist" class="tabs tabs-border">
      <For each={tabs}>
        {(tab) => (
          <button
            type="button"
            role="tab"
            class="tab"
            classList={{ "tab-active": current() === tab.path }}
            aria-selected={current() === tab.path}
            onClick={() => navigate(tab.path)}
          >
            {t(tab.key)}
          </button>
        )}
      </For>
    </div>
  );
}

export function App() {
  const api = useApi();
  const session = useSession();
  const { t } = useI18n();
  const [status, { refetch }] = createResource(() => api.get<Status>("/status"));
  // after "rc-service docker start" or a new group membership the user comes back to this window: check again
  onMount(() => {
    const check = () => void refetch();
    window.addEventListener("focus", check);
    onCleanup(() => window.removeEventListener("focus", check));
  });
  // admin mode changes who the backend runs as; a page whose request got docker_unavailable or docker_forbidden (see
  // gate.ts) says that Docker changed since the last check, so the hint panel can replace the page
  createEffect(on([() => session.admin(), dockerErrors], () => void refetch(), { defer: true }));
  // while it refreshes, the resource keeps its last value, so the page does not flicker
  const current = () => (status.error ? undefined : status());
  const blocked = () => {
    const s = current();
    return s && s.docker !== "ok" ? s : undefined;
  };
  return (
    <div class="flex flex-col gap-4">
      <PageHeader
        title={t("title")}
        subtitle={
          current()?.version ? t("subtitleVersion", { version: current()?.version ?? "" }) : t("subtitle")
        }
      />
      <Switch fallback={<LoadingState />}>
        <Match when={status.error}>
          <ErrorAlert error={status.error} onRetry={() => void refetch()} />
        </Match>
        <Match when={blocked()}>{(s) => <AccessPanel status={s()} onRetry={() => void refetch()} />}</Match>
        <Match when={current()?.docker === "ok"}>
          <Tabs />
          <ExtensionRouter routes={routes} fallback={<p class="text-base-content/70">{t("notFound")}</p>} />
        </Match>
      </Switch>
    </div>
  );
}
