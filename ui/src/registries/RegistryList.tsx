import {
  ConfirmDialog,
  ErrorAlert,
  LoadingState,
  useApi,
  useI18n,
  useSession,
  useToast,
} from "@edelweiss/sdk";
import { createEffect, createResource, createSignal, For, Match, on, Show, Switch } from "solid-js";
import type { RegistryList as List, Registry, TestResult } from "../api";
import { errorText } from "../errors";
import { RegistryDialog } from "./RegistryDialog";

export function RegistryList() {
  const api = useApi();
  const session = useSession();
  const { t } = useI18n();
  const toast = useToast();
  const [list, { refetch }] = createResource(() => api.get<List>("/registries"));
  // admin mode decides whether the list may be changed (and who reads it)
  createEffect(
    on(
      () => session.admin(),
      () => void refetch(),
      { defer: true },
    ),
  );
  const [editing, setEditing] = createSignal<Registry | "new">();
  const [removing, setRemoving] = createSignal<Registry>();
  const [testing, setTesting] = createSignal<string>();
  const data = () => (list.error ? undefined : list());
  const writable = () => data()?.writable ?? false;

  const test = async (r: Registry) => {
    setTesting(r.id);
    try {
      const res = await api.post<TestResult>("/registries/test", { id: r.id });
      if (res.ok) toast.success(t("registries.testOk"));
      else toast.error(t("registries.testFailed", { message: res.message }));
    } catch (err) {
      toast.error(errorText(err, t));
    } finally {
      setTesting(undefined);
    }
  };
  const remove = async () => {
    const r = removing();
    setRemoving(undefined);
    if (!r) return;
    try {
      await api.del(`/registries/${encodeURIComponent(r.id)}`);
      toast.success(t("registries.deleted", { name: r.name }));
    } catch (err) {
      toast.error(errorText(err, t));
    }
    void refetch();
  };

  return (
    <section class="flex flex-col gap-3">
      <Show when={data() && !writable()}>
        <div role="status" class="alert alert-info flex flex-wrap justify-between">
          <span>{t("registries.readOnly")}</span>
          <Show when={session.canAdmin()}>
            <button type="button" class="btn btn-sm" onClick={() => void session.requestAdmin()}>
              {t("access.adminMode")}
            </button>
          </Show>
        </div>
      </Show>
      <Show when={writable()}>
        <div class="flex justify-end">
          <button type="button" class="btn btn-sm btn-primary" onClick={() => setEditing("new")}>
            {t("registries.add")}
          </button>
        </div>
      </Show>
      <Switch fallback={<LoadingState />}>
        <Match when={list.error}>
          <ErrorAlert error={errorText(list.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={data()?.registries.length === 0}>
          <p class="text-base-content/70">{t("registries.empty")}</p>
        </Match>
        <Match when={data()}>
          {(d) => (
            <div class="overflow-x-auto">
              <table class="table table-sm">
                <thead>
                  <tr>
                    <th>{t("registries.name")}</th>
                    <th>{t("registries.address")}</th>
                    <th>{t("registries.login")}</th>
                    <th>
                      <span class="sr-only">{t("containers.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <For each={d().registries}>
                    {(r) => (
                      <tr>
                        <td class="font-medium">{r.name}</td>
                        <td class="font-mono text-sm">{r.address}</td>
                        <td class="text-sm">
                          <Show
                            when={r.username}
                            fallback={
                              <span class="text-base-content/70">{t("registries.withoutLogin")}</span>
                            }
                          >
                            {r.username}
                          </Show>
                        </td>
                        <td>
                          <div class="flex justify-end gap-1">
                            <button
                              type="button"
                              class="btn btn-xs"
                              disabled={testing() === r.id}
                              aria-label={`${t("registries.test")} ${r.name}`}
                              onClick={() => void test(r)}
                            >
                              {t("registries.test")}
                            </button>
                            <Show when={writable()}>
                              <button
                                type="button"
                                class="btn btn-xs"
                                aria-label={`${t("actions.edit")} ${r.name}`}
                                onClick={() => setEditing(r)}
                              >
                                {t("actions.edit")}
                              </button>
                              <button
                                type="button"
                                class="btn btn-xs btn-ghost text-error"
                                aria-label={`${t("actions.delete")} ${r.name}`}
                                onClick={() => setRemoving(r)}
                              >
                                {t("actions.delete")}
                              </button>
                            </Show>
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
      <Show when={editing()} keyed>
        {(e) => (
          <RegistryDialog
            entry={e === "new" ? undefined : e}
            onClose={(saved) => {
              setEditing(undefined);
              if (saved) void refetch();
            }}
          />
        )}
      </Show>
      <ConfirmDialog
        open={removing() !== undefined}
        title={t("registries.deleteTitle")}
        message={t("registries.deleteMessage", { name: removing()?.name ?? "" })}
        confirmLabel={t("actions.delete")}
        danger
        onConfirm={() => void remove()}
        onCancel={() => setRemoving(undefined)}
      />
    </section>
  );
}
