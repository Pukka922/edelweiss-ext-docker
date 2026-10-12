import {
  ConfirmDialog,
  ErrorAlert,
  LoadingState,
  useApi,
  useExtensionNavigate,
  useI18n,
  useToast,
} from "@edelweiss/sdk";
import { IconTrash } from "@tabler/icons-solidjs";
import { createResource, createSignal, For, Match, Show, Switch } from "solid-js";
import type { NetworkInfo } from "../api";
import { IconButton } from "../components/IconButton";
import { errorText } from "../errors";
import { onDockerChange } from "../live";
import { NetworkDialog } from "./NetworkDialog";

export function NetworkList() {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  const navigate = useExtensionNavigate();
  const [list, { refetch }] = createResource(() => api.get<NetworkInfo[]>("/networks"));
  onDockerChange(["network", "container"], () => void refetch());
  const [creating, setCreating] = createSignal(false);
  const [removing, setRemoving] = createSignal<NetworkInfo>();
  const items = () => (list.error ? undefined : list());

  const remove = async () => {
    const n = removing();
    setRemoving(undefined);
    if (!n) return;
    try {
      await api.del(`/networks/${encodeURIComponent(n.id)}`);
      toast.success(t("networks.deleted", { name: n.name }));
    } catch (err) {
      toast.error(errorText(err, t));
    }
    void refetch();
  };

  return (
    <section class="flex flex-col gap-3">
      <div class="flex justify-end">
        <button type="button" class="btn btn-sm btn-primary" onClick={() => setCreating(true)}>
          {t("networks.create")}
        </button>
      </div>
      <Switch fallback={<LoadingState />}>
        <Match when={list.error}>
          <ErrorAlert error={errorText(list.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={items()?.length === 0}>
          <p class="text-base-content/70">{t("networks.empty")}</p>
        </Match>
        <Match when={items()}>
          {(all) => (
            <div class="overflow-x-auto">
              <table class="table table-sm">
                <thead>
                  <tr>
                    <th>{t("networks.name")}</th>
                    <th>{t("networks.driver")}</th>
                    <th>{t("networks.scope")}</th>
                    <th>{t("networks.subnet")}</th>
                    <th>{t("networks.gateway")}</th>
                    <th>{t("networks.containers")}</th>
                    <th>
                      <span class="sr-only">{t("containers.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={all()}>
                    {(n) => (
                      <tr>
                        <td>
                          <button
                            type="button"
                            class="link link-hover font-medium"
                            onClick={() => navigate(`/networks/${n.id}`)}
                          >
                            {n.name}
                          </button>
                          <Show when={n.predefined}>
                            <span class="badge badge-ghost badge-sm ml-2">{t("networks.predefined")}</span>
                          </Show>
                          <Show when={n.internal}>
                            <span class="badge badge-info badge-sm ml-2">{t("networks.internalShort")}</span>
                          </Show>
                        </td>
                        <td class="text-sm">{n.driver}</td>
                        <td class="text-sm">{n.scope}</td>
                        <td class="font-mono text-xs">{n.subnet ?? ""}</td>
                        <td class="font-mono text-xs">{n.gateway ?? ""}</td>
                        <td class="text-sm">{n.containers}</td>
                        <td>
                          <div class="flex justify-end">
                            <IconButton
                              icon={IconTrash}
                              label={`${t("actions.delete")} ${n.name}`}
                              tooltip={n.predefined ? t("errors.network_predefined") : t("actions.delete")}
                              danger
                              disabled={n.predefined}
                              onClick={() => setRemoving(n)}
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
      <Show when={creating()}>
        <NetworkDialog
          onClose={(created) => {
            setCreating(false);
            if (created) void refetch();
          }}
        />
      </Show>
      <ConfirmDialog
        open={removing() !== undefined}
        title={t("networks.deleteTitle")}
        message={t("networks.deleteMessage", { name: removing()?.name ?? "" })}
        confirmLabel={t("actions.delete")}
        danger
        onConfirm={() => void remove()}
        onCancel={() => setRemoving(undefined)}
      />
    </section>
  );
}
