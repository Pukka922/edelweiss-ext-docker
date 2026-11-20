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
import { type ImageInfo, shortId } from "../api";
import { IconButton } from "../components/IconButton";
import { Pager } from "../components/Pager";
import { SearchInput } from "../components/SearchInput";
import { errorText } from "../errors";
import { formatBytes, formatDate } from "../format";
import { compareNames, useList } from "../listing";
import { onDockerChange } from "../live";
import { PullDialog } from "./PullDialog";

/** The name an image is shown by: its first tag, else its short ID. */
const nameOf = (i: ImageInfo) => i.tags[0] ?? shortId(i.id);

export function ImageList() {
  const api = useApi();
  const { t, locale } = useI18n();
  const toast = useToast();
  const navigate = useExtensionNavigate();
  const [list, { refetch }] = createResource(() => api.get<ImageInfo[]>("/images"));
  onDockerChange(["image", "container"], () => void refetch());
  const [unusedOnly, setUnusedOnly] = createSignal(false);
  const [removing, setRemoving] = createSignal<ImageInfo>();
  const [pruning, setPruning] = createSignal(false);
  const [pulling, setPulling] = createSignal(false);
  const items = () => (list.error ? undefined : list());
  const unused = () => (items() ?? []).filter((i) => i.usedBy.length === 0);
  // tags and users by name; the images by their first tag, the untagged ones last, by ID
  const sorted = createMemo(() =>
    items()
      ?.map((i) => ({
        ...i,
        tags: i.tags.slice().sort(compareNames),
        usedBy: i.usedBy.slice().sort((a, b) => compareNames(a.name, b.name)),
      }))
      .sort((a, b) =>
        a.tags.length > 0 && b.tags.length > 0
          ? compareNames(a.tags[0] ?? "", b.tags[0] ?? "") || compareNames(a.id, b.id)
          : a.tags.length > 0 || b.tags.length > 0
            ? a.tags.length > 0
              ? -1
              : 1
            : compareNames(a.id, b.id),
      ),
  );
  const view = useList(
    "images",
    sorted,
    (i) => [...i.tags, shortId(i.id), ...i.usedBy.map((u) => u.name)],
    (i) => !unusedOnly() || i.usedBy.length === 0,
  );
  const removingName = () => {
    const i = removing();
    return i ? nameOf(i) : "";
  };

  const remove = async () => {
    const i = removing();
    setRemoving(undefined);
    if (!i) return;
    try {
      // the backend takes only the full ID (sha256:…), never a tag or a short ID
      await api.del(`/images/${encodeURIComponent(i.id)}`);
      toast.success(t("images.deleted"));
    } catch (err) {
      toast.error(errorText(err, t));
    }
    void refetch();
  };
  const askPrune = () => {
    if (unused().length === 0) toast.info(t("images.nothingToPrune"));
    else setPruning(true);
  };
  const prune = async () => {
    setPruning(false);
    try {
      const res = await api.post<{ deleted: number; spaceReclaimed: number }>("/images/prune");
      toast.success(
        t("images.pruned", { count: res.deleted, size: formatBytes(res.spaceReclaimed, locale()) }),
      );
    } catch (err) {
      toast.error(errorText(err, t));
    }
    void refetch();
  };

  return (
    <section class="flex flex-col gap-3">
      <div class="flex flex-wrap items-center gap-2">
        <SearchInput value={view.search()} onInput={view.setSearch} label={t("images.search")} />
        <label class="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked={unusedOnly()}
            onChange={(e) => {
              setUnusedOnly(e.currentTarget.checked);
              view.setPage(1);
            }}
          />
          {t("images.unusedOnly")}
        </label>
        <div class="ml-auto flex gap-2">
          <button type="button" class="btn btn-sm" disabled={!items()} onClick={askPrune}>
            {t("images.prune")}
          </button>
          <button type="button" class="btn btn-sm btn-primary" onClick={() => setPulling(true)}>
            {t("images.pull")}
          </button>
        </div>
      </div>
      <Switch fallback={<LoadingState />}>
        <Match when={list.error}>
          <ErrorAlert error={errorText(list.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={items()?.length === 0}>
          <p class="text-base-content/70">{t("images.empty")}</p>
        </Match>
        <Match when={items() && view.count() === 0 && view.search().trim() !== ""}>
          <p class="text-base-content/70">{t("list.noMatch", { query: view.search().trim() })}</p>
        </Match>
        <Match when={items()}>
          <div class="overflow-x-auto">
            <table class="table table-sm">
              <thead>
                <tr>
                  <th>{t("images.tags")}</th>
                  <th>{t("images.id")}</th>
                  <th>{t("images.size")}</th>
                  <th>{t("images.created")}</th>
                  <th>{t("images.usage")}</th>
                  <th>
                    <span class="sr-only">{t("containers.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                <For each={view.rows()}>
                  {(i) => (
                    <tr>
                      <td>
                        <Show
                          when={i.tags.length > 0}
                          fallback={<span class="text-base-content/50">{t("images.untagged")}</span>}
                        >
                          <For each={i.tags}>{(tag) => <div class="break-all">{tag}</div>}</For>
                        </Show>
                      </td>
                      <td class="font-mono text-xs">{shortId(i.id)}</td>
                      <td class="text-sm">{formatBytes(i.size, locale())}</td>
                      <td class="text-sm">{formatDate(i.created, locale())}</td>
                      <td class="text-sm">
                        <Show
                          when={i.usedBy.length > 0}
                          fallback={<span class="badge badge-ghost badge-sm">{t("images.unused")}</span>}
                        >
                          <For each={i.usedBy}>
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
                            label={`${t("actions.delete")} ${nameOf(i)}`}
                            tooltip={i.usedBy.length > 0 ? t("errors.image_in_use") : t("actions.delete")}
                            danger
                            disabled={i.usedBy.length > 0}
                            onClick={() => setRemoving(i)}
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
      <ConfirmDialog
        open={removing() !== undefined}
        title={t("images.deleteTitle")}
        message={t("images.deleteMessage", { name: removingName() })}
        confirmLabel={t("actions.delete")}
        danger
        onConfirm={() => void remove()}
        onCancel={() => setRemoving(undefined)}
      />
      <ConfirmDialog
        open={pruning()}
        title={t("images.pruneTitle")}
        message={t("images.pruneMessage", {
          count: unused().length,
          size: formatBytes(
            unused().reduce((sum, i) => sum + i.size, 0),
            locale(),
          ),
        })}
        confirmLabel={t("images.prune")}
        danger
        onConfirm={() => void prune()}
        onCancel={() => setPruning(false)}
      />
      <Show when={pulling()}>
        <PullDialog
          onClose={(pulled) => {
            setPulling(false);
            if (pulled) void refetch();
          }}
        />
      </Show>
    </section>
  );
}
