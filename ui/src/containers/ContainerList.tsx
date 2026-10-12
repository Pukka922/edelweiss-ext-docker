import { ErrorAlert, LoadingState, useApi, useExtensionNavigate, useI18n } from "@edelweiss/sdk";
import { IconPencil, IconPlayerPlay, IconPlayerStop, IconRefresh, IconTrash } from "@tabler/icons-solidjs";
import { createResource, createSignal, For, Match, Show, Switch } from "solid-js";
import type { ContainerSummary } from "../api";
import { IconButton } from "../components/IconButton";
import { errorText } from "../errors";
import { onDockerChange } from "../live";
import { isActive, portText, stateBadge, useContainerActions } from "./actions";
import { DeleteContainerDialog } from "./DeleteContainerDialog";

export function ContainerList() {
  const api = useApi();
  const { t } = useI18n();
  const navigate = useExtensionNavigate();
  const actions = useContainerActions();
  const [list, { refetch }] = createResource(() => api.get<ContainerSummary[]>("/containers"));
  onDockerChange(["container"], () => void refetch());
  const [removing, setRemoving] = createSignal<ContainerSummary>();
  const items = () => (list.error ? undefined : list());

  const act = async (c: ContainerSummary, action: "start" | "stop" | "restart") => {
    await actions.run(c, action);
    void refetch();
  };
  const remove = async (withVolumes: boolean) => {
    const c = removing();
    setRemoving(undefined);
    if (c && (await actions.remove(c, withVolumes))) void refetch();
  };

  return (
    <section class="flex flex-col gap-3">
      <div class="flex justify-end">
        <button type="button" class="btn btn-sm btn-primary" onClick={() => navigate("/containers/new")}>
          {t("containers.create")}
        </button>
      </div>
      <Switch fallback={<LoadingState />}>
        <Match when={list.error}>
          <ErrorAlert error={errorText(list.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={items()?.length === 0}>
          <p class="text-base-content/70">{t("containers.empty")}</p>
        </Match>
        <Match when={items()}>
          {(rows) => (
            <div class="overflow-x-auto">
              <table class="table table-sm">
                <thead>
                  <tr>
                    <th>{t("containers.name")}</th>
                    <th>{t("containers.image")}</th>
                    <th>{t("containers.state")}</th>
                    <th>{t("containers.ports")}</th>
                    <th>
                      <span class="sr-only">{t("containers.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={rows()}>
                    {(c) => (
                      <tr>
                        <td>
                          <button
                            type="button"
                            class="link link-hover font-medium"
                            onClick={() => navigate(`/containers/${c.id}`)}
                          >
                            {c.name}
                          </button>
                        </td>
                        <td class="max-w-xs truncate" title={c.image}>
                          {c.image}
                        </td>
                        <td>
                          <span class={stateBadge(c.state)}>{t(`state.${c.state}`)}</span>
                          <div class="text-xs text-base-content/70">{c.status}</div>
                        </td>
                        <td class="text-sm">
                          <For each={c.ports.filter((p) => p.hostPort)}>
                            {(p) => <div>{portText(p, t)}</div>}
                          </For>
                        </td>
                        <td>
                          <div class="flex justify-end gap-1">
                            <Show
                              when={isActive(c.state)}
                              fallback={
                                <IconButton
                                  icon={IconPlayerPlay}
                                  label={`${t("actions.start")} ${c.name}`}
                                  tooltip={t("actions.start")}
                                  disabled={actions.busy(c.id)}
                                  onClick={() => void act(c, "start")}
                                />
                              }
                            >
                              <IconButton
                                icon={IconPlayerStop}
                                label={`${t("actions.stop")} ${c.name}`}
                                tooltip={t("actions.stop")}
                                disabled={actions.busy(c.id)}
                                onClick={() => void act(c, "stop")}
                              />
                              <IconButton
                                icon={IconRefresh}
                                label={`${t("actions.restart")} ${c.name}`}
                                tooltip={t("actions.restart")}
                                disabled={actions.busy(c.id)}
                                onClick={() => void act(c, "restart")}
                              />
                            </Show>
                            <IconButton
                              icon={IconPencil}
                              label={`${t("actions.edit")} ${c.name}`}
                              tooltip={t("actions.edit")}
                              onClick={() => navigate(`/containers/${c.id}/edit`)}
                            />
                            <IconButton
                              icon={IconTrash}
                              label={`${t("actions.delete")} ${c.name}`}
                              tooltip={t("actions.delete")}
                              danger
                              disabled={actions.busy(c.id)}
                              onClick={() => setRemoving(c)}
                            />
                          </div>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
            </div>
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
