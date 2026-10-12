import { ErrorAlert, LoadingState, useApi, useExtensionNavigate, useI18n } from "@edelweiss/sdk";
import { createResource, createSignal, For, type JSX, Match, onCleanup, Show, Switch } from "solid-js";
import type { ContainerInfo, ContainerDetail as Detail } from "../api";
import { DetailRow } from "../components/DetailRow";
import { errorText } from "../errors";
import { formatDate } from "../format";
import { onDockerChange } from "../live";
import { isActive, portText, stateBadge, useContainerActions } from "./actions";
import { ConsoleTab } from "./ConsoleTab";
import { DeleteContainerDialog } from "./DeleteContainerDialog";
import { LogsTab } from "./LogsTab";
import { StatsTab } from "./StatsTab";

type Tab = "overview" | "logs" | "stats" | "console";

const tabs: Tab[] = ["overview", "logs", "stats", "console"];

/**
 * Whether statistics and console can work: the container runs. Docker's State.Running is also true for a restarting
 * and a paused container, which have neither (the backend sends "stopped" for a restarting one); only the status says.
 */
const runs = (c: Detail) => c.state.status === "running";

/**
 * Builds its content anew with every start of the container. A stream of the tabs ends with the run of the container,
 * and the page does not always see the stop in between (a quick restart can fall between two reloads), but startedAt
 * changes with every start.
 */
function EachRun(props: { startedAt: string; children: () => JSX.Element }) {
  return <For each={[props.startedAt]}>{() => props.children()}</For>;
}

function Overview(props: { c: Detail }) {
  const { t, locale } = useI18n();
  const none = () => <span class="text-base-content/50">{t("detail.none")}</span>;
  return (
    <dl class="divide-y divide-base-200">
      <DetailRow label={t("detail.image")}>{props.c.image}</DetailRow>
      <DetailRow label={t("detail.command")}>
        <code class="break-all">{props.c.command}</code>
      </DetailRow>
      <DetailRow label={t("detail.created")}>{formatDate(props.c.created, locale())}</DetailRow>
      <DetailRow label={t("detail.startedAt")}>{formatDate(props.c.state.startedAt, locale())}</DetailRow>
      <Show when={!props.c.state.running}>
        <DetailRow label={t("detail.finishedAt")}>{formatDate(props.c.state.finishedAt, locale())}</DetailRow>
        <DetailRow label={t("detail.exitCode")}>{props.c.state.exitCode}</DetailRow>
      </Show>
      <Show when={props.c.state.health}>
        <DetailRow label={t("detail.health")}>{props.c.state.health}</DetailRow>
      </Show>
      <DetailRow label={t("detail.restart")}>{t(`form.policies.${props.c.restart.policy}`)}</DetailRow>
      <DetailRow label={t("detail.ports")}>
        <Show when={props.c.ports.length > 0} fallback={none()}>
          <For each={props.c.ports}>{(p) => <div>{portText(p, t)}</div>}</For>
        </Show>
      </DetailRow>
      <DetailRow label={t("detail.mounts")}>
        <Show when={props.c.mounts.length > 0} fallback={none()}>
          <For each={props.c.mounts}>
            {(m) => (
              <div>
                {`${m.anonymous ? `${m.source.slice(0, 12)} (${t("detail.anonymous")})` : m.source || m.type} → ${m.target}${m.readOnly ? ` (${t("detail.readOnly")})` : ""}`}
              </div>
            )}
          </For>
        </Show>
      </DetailRow>
      <DetailRow label={t("detail.networks")}>
        <Show when={props.c.networks.length > 0} fallback={none()}>
          <For each={props.c.networks}>{(n) => <div>{n.ipv4 ? `${n.name}: ${n.ipv4}` : n.name}</div>}</For>
        </Show>
      </DetailRow>
      <DetailRow label={t("detail.env")}>
        <Show when={props.c.env.length > 0} fallback={none()}>
          <For each={props.c.env}>{(e) => <div class="break-all font-mono text-xs">{e}</div>}</For>
        </Show>
      </DetailRow>
    </dl>
  );
}

export function ContainerDetail(props: { params: Record<string, string> }) {
  const api = useApi();
  const { t } = useI18n();
  const navigate = useExtensionNavigate();
  const actions = useContainerActions();
  const id = () => props.params.id ?? "";
  const [info, { refetch }] = createResource(id, (i) =>
    api.get<ContainerInfo>(`/containers/${encodeURIComponent(i)}`),
  );
  onDockerChange(["container"], () => void refetch());
  const [tab, setTab] = createSignal<Tab>("overview");
  // the container picked at the click: a reload while the dialog is open must not replace it (that would reset the dialog)
  const [removing, setRemoving] = createSignal<{ id: string; name: string }>();
  const loaded = () => (info.error ? undefined : info());

  const act = async (action: "start" | "stop" | "restart") => {
    const c = loaded()?.container;
    if (!c) return;
    await actions.run(c, action);
    void refetch();
  };
  // the delete request outlives a click on "back" or a tab of the shell: then there is no page to leave any more
  let mounted = true;
  onCleanup(() => {
    mounted = false;
  });
  const remove = async (withVolumes: boolean) => {
    const c = removing();
    setRemoving(undefined);
    if (c && (await actions.remove(c, withVolumes)) && mounted) navigate("/containers");
  };

  return (
    <section class="flex flex-col gap-3">
      <button
        type="button"
        class="link link-hover self-start text-sm"
        onClick={() => navigate("/containers")}
      >
        ← {t("detail.back")}
      </button>
      <Switch fallback={<LoadingState />}>
        <Match when={info.error}>
          <ErrorAlert error={errorText(info.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={loaded()}>
          {(i) => (
            <>
              <div class="flex flex-wrap items-center gap-2">
                <h2 class="text-xl font-semibold">{i().container.name}</h2>
                <span class={stateBadge(i().container.state.status)}>
                  {t(`state.${i().container.state.status}`)}
                </span>
                <div class="ml-auto flex flex-wrap gap-1">
                  <Show
                    when={isActive(i().container.state.status)}
                    fallback={
                      <button
                        type="button"
                        class="btn btn-sm"
                        disabled={actions.busy(i().container.id)}
                        onClick={() => void act("start")}
                      >
                        {t("actions.start")}
                      </button>
                    }
                  >
                    <button
                      type="button"
                      class="btn btn-sm"
                      disabled={actions.busy(i().container.id)}
                      onClick={() => void act("stop")}
                    >
                      {t("actions.stop")}
                    </button>
                    <button
                      type="button"
                      class="btn btn-sm"
                      disabled={actions.busy(i().container.id)}
                      onClick={() => void act("restart")}
                    >
                      {t("actions.restart")}
                    </button>
                  </Show>
                  <button
                    type="button"
                    class="btn btn-sm"
                    onClick={() => navigate(`/containers/${i().container.id}/edit`)}
                  >
                    {t("actions.edit")}
                  </button>
                  <button
                    type="button"
                    class="btn btn-sm btn-ghost text-error"
                    disabled={actions.busy(i().container.id)}
                    onClick={() => setRemoving({ id: i().container.id, name: i().container.name })}
                  >
                    {t("actions.delete")}
                  </button>
                </div>
              </div>
              <div role="tablist" class="tabs tabs-box">
                <For each={tabs}>
                  {(name) => (
                    <button
                      type="button"
                      role="tab"
                      class="tab"
                      classList={{ "tab-active": tab() === name }}
                      aria-selected={tab() === name}
                      onClick={() => setTab(name)}
                    >
                      {t(`detail.tabs.${name}`)}
                    </button>
                  )}
                </For>
              </div>
              <Switch>
                <Match when={tab() === "overview"}>
                  <Overview c={i().container} />
                </Match>
                <Match when={tab() === "logs"}>
                  <EachRun startedAt={i().container.state.startedAt}>
                    {() => <LogsTab id={i().container.id} running={runs(i().container)} />}
                  </EachRun>
                </Match>
                <Match when={tab() === "stats"}>
                  <EachRun startedAt={i().container.state.startedAt}>
                    {() => <StatsTab id={i().container.id} running={runs(i().container)} />}
                  </EachRun>
                </Match>
                <Match when={tab() === "console"}>
                  <ConsoleTab id={i().container.id} running={runs(i().container)} />
                </Match>
              </Switch>
            </>
          )}
        </Match>
      </Switch>
      <DeleteContainerDialog
        target={removing()}
        onCancel={() => setRemoving(undefined)}
        onConfirm={(v) => void remove(v)}
      />
    </section>
  );
}
