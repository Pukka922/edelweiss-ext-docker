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
import { createMemo, createResource, createSignal, For, Match, Show, Switch } from "solid-js";
import { shortId, type Volume } from "../api";
import { IconButton } from "../components/IconButton";
import { Pager } from "../components/Pager";
import { SearchInput } from "../components/SearchInput";
import { errorText } from "../errors";
import { formatDate } from "../format";
import { compareNames, useList } from "../listing";
import { onDockerChange } from "../live";
import { VolumeDialog } from "./VolumeDialog";

/** Anonymous volumes have 64-character names; they are shown by their first 12. */
const nameOf = (v: Volume) => (v.anonymous ? shortId(v.name) : v.name);

export function VolumeList() {
  const api = useApi();
  const { t, locale } = useI18n();
  const toast = useToast();
  const navigate = useExtensionNavigate();
  const [list, { refetch }] = createResource(() => api.get<Volume[]>("/volumes"));
  onDockerChange(["volume", "container"], () => void refetch());
  const [creating, setCreating] = createSignal(false);
  const [removing, setRemoving] = createSignal<Volume>();
  const items = () => (list.error ? undefined : list());
  // named volumes by name, the anonymous ones last, also by name; the users by name
  const sorted = createMemo(() =>
    items()
      ?.map((v) => ({ ...v, usedBy: v.usedBy.slice().sort((a, b) => compareNames(a.name, b.name)) }))
      .sort((a, b) => Number(a.anonymous) - Number(b.anonymous) || compareNames(a.name, b.name)),
  );
  const view = useList("volumes", sorted, (v) => [v.name, v.driver, ...v.usedBy.map((u) => u.name)]);
  const removingName = () => {
    const v = removing();
    return v ? nameOf(v) : "";
  };

  const remove = async () => {
    const v = removing();
    setRemoving(undefined);
    if (!v) return;
    try {
      await api.del(`/volumes/${encodeURIComponent(v.name)}`);
      toast.success(t("volumes.deleted", { name: nameOf(v) }));
    } catch (err) {
      toast.error(errorText(err, t));
    }
    void refetch();
  };

  return (
    <section class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center gap-2">
        <SearchInput value={view.search()} onInput={view.setSearch} label={t("volumes.search")} />
        <button type="button" class="btn btn-sm btn-primary ml-auto" onClick={() => setCreating(true)}>
          {t("volumes.create")}
        </button>
      </div>
      <Switch fallback={<LoadingState />}>
        <Match when={list.error}>
          <ErrorAlert error={errorText(list.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={items()?.length === 0}>
          <p class="text-base-content/70">{t("volumes.empty")}</p>
        </Match>
        <Match when={items() && view.count() === 0}>
          <p class="text-base-content/70">{t("list.noMatch", { query: view.search().trim() })}</p>
        </Match>
        <Match when={items()}>
          <div class="overflow-x-auto">
            <table class="table table-sm">
              <thead>
                <tr>
                  <th>{t("volumes.name")}</th>
                  <th>{t("volumes.driver")}</th>
                  <th>{t("volumes.mountpoint")}</th>
                  <th>{t("volumes.created")}</th>
                  <th>{t("volumes.usage")}</th>
                  <th>
                    <span class="sr-only">{t("containers.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                <For each={view.rows()}>
                  {(v) => (
                    <tr>
                      <td>
                        <span class="font-medium" title={v.name}>
                          {nameOf(v)}
                        </span>
                        <Show when={v.anonymous}>
                          <span class="badge badge-ghost badge-sm ml-2">{t("volumes.anonymous")}</span>
                        </Show>
                      </td>
                      <td class="text-sm">{v.driver}</td>
                      <td class="max-w-xs truncate font-mono text-xs" title={v.mountpoint}>
                        {v.mountpoint}
                      </td>
                      <td class="text-sm">{formatDate(v.createdAt, locale())}</td>
                      <td class="text-sm">
                        <Show
                          when={v.usedBy.length > 0}
                          fallback={<span class="badge badge-ghost badge-sm">{t("volumes.unused")}</span>}
                        >
                          <For each={v.usedBy}>
                            {(u) => (
                              <div>
                                <button
                                  type="button"
                                  class="link link-hover"
                                  onClick={() => navigate(`/containers/${u.id}`)}
                                >
                                  {u.name}
                                </button>
                              </div>
                            )}
                          </For>
                        </Show>
                      </td>
                      <td>
                        <div class="flex justify-end">
                          <IconButton
                            icon={IconTrash}
                            label={`${t("actions.delete")} ${nameOf(v)}`}
                            tooltip={v.usedBy.length > 0 ? t("errors.volume_in_use") : t("actions.delete")}
                            danger
                            disabled={v.usedBy.length > 0}
                            onClick={() => setRemoving(v)}
                          />
                        </div>
                      </td>
                    </tr>
                  )}
                </For>
              </tbody>
            </table>
          </div>
          <Pager page={view.page()} pages={view.pages()} onPage={view.setPage} />
        </Match>
      </Switch>
      <Show when={creating()}>
        <VolumeDialog
          onClose={(created) => {
            setCreating(false);
            if (created) void refetch();
          }}
        />
      </Show>
      <ConfirmDialog
        open={removing() !== undefined}
        title={t("volumes.deleteTitle")}
        message={t("volumes.deleteMessage", { name: removingName() })}
        confirmLabel={t("actions.delete")}
        danger
        onConfirm={() => void remove()}
        onCancel={() => setRemoving(undefined)}
      />
    </section>
  );
}
