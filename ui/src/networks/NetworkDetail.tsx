import {
  ConfirmDialog,
  ErrorAlert,
  LoadingState,
  useApi,
  useExtensionNavigate,
  useI18n,
  useToast,
} from "@edelweiss/sdk";
import { IconPlugConnectedX } from "@tabler/icons-solidjs";
import { createEffect, createResource, createSignal, For, Match, onCleanup, Show, Switch } from "solid-js";
import type { ContainerSummary, NetworkDetail as Detail } from "../api";
import { DetailRow } from "../components/DetailRow";
import { Input, Select } from "../components/Field";
import { Field } from "../components/FormDialog";
import { IconButton } from "../components/IconButton";
import { errorField, errorText } from "../errors";
import { onDockerChange } from "../live";

export function NetworkDetail(props: { params: Record<string, string> }) {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  const navigate = useExtensionNavigate();
  const [net, { refetch }] = createResource(
    () => props.params.id ?? "",
    (id) => api.get<Detail>(`/networks/${encodeURIComponent(id)}`),
  );
  // the containers the connect form offers; a failure is shown in the form (a failed resource throws when read)
  const [containers, { refetch: refetchContainers }] = createResource(() =>
    api.get<ContainerSummary[]>("/containers"),
  );
  onDockerChange(["network", "container"], () => {
    void refetch();
    void refetchContainers();
  });
  const [chosen, setChosen] = createSignal("");
  const [ipv4, setIpv4] = createSignal("");
  const [busy, setBusy] = createSignal(false);
  const [connectError, setConnectError] = createSignal<unknown>();
  const [removing, setRemoving] = createSignal(false);
  const loaded = () => (net.error ? undefined : net());
  const connectable = () => {
    if (containers.error) return [];
    const attached = new Set((loaded()?.attached ?? []).map((a) => a.id));
    return (containers() ?? []).filter((c) => !attached.has(c.id));
  };
  const selected = () => connectable().find((c) => c.id === chosen());
  // a container that is no longer offered (removed, or connected meanwhile) must not stay chosen: Connect would do nothing
  createEffect(() => {
    if (chosen() !== "" && !selected()) setChosen("");
  });
  const invalid = (field: string) => errorField(connectError()) === field;
  // a mark says what the last attempt got wrong; editing that field takes it away
  const edited = (field: string) => {
    if (invalid(field)) setConnectError(undefined);
  };
  const path = (n: Detail, action: string) => `/networks/${encodeURIComponent(n.id)}/${action}`;

  const connect = async (n: Detail) => {
    const c = selected();
    if (!c) return;
    setBusy(true);
    setConnectError(undefined);
    try {
      await api.post(path(n, "connect"), { container: c.id, ipv4: ipv4().trim() });
      toast.success(t("networks.connected", { name: c.name }));
      setChosen("");
      setIpv4("");
    } catch (err) {
      setConnectError(err);
      toast.error(errorText(err, t));
    } finally {
      setBusy(false);
    }
    void refetch();
  };
  const disconnect = async (n: Detail, a: { id: string; name: string }) => {
    setBusy(true);
    try {
      await api.post(path(n, "disconnect"), { container: a.id });
      toast.success(t("networks.disconnected", { name: a.name }));
    } catch (err) {
      toast.error(errorText(err, t));
    } finally {
      setBusy(false);
    }
    void refetch();
  };
  // the delete request outlives a click on "back" or a tab of the shell: then there is no page to leave any more
  let mounted = true;
  onCleanup(() => {
    mounted = false;
  });
  const remove = async () => {
    setRemoving(false);
    const n = loaded();
    if (!n) return;
    try {
      await api.del(`/networks/${encodeURIComponent(n.id)}`);
      toast.success(t("networks.deleted", { name: n.name }));
      if (mounted) navigate("/networks");
    } catch (err) {
      toast.error(errorText(err, t));
    }
  };

  return (
    <section class="flex flex-col gap-3">
      <button type="button" class="link link-hover self-start text-sm" onClick={() => navigate("/networks")}>
        ← {t("networks.back")}
      </button>
      <Switch fallback={<LoadingState />}>
        <Match when={net.error}>
          <ErrorAlert error={errorText(net.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={loaded()}>
          {(n) => (
            <>
              <div class="flex flex-wrap items-center gap-2">
                <h2 class="text-xl font-semibold">{n().name}</h2>
                <Show when={n().predefined}>
                  <span class="badge badge-ghost badge-sm">{t("networks.predefined")}</span>
                </Show>
                <Show when={n().internal}>
                  <span class="badge badge-info badge-sm">{t("networks.internalShort")}</span>
                </Show>
                <Show when={!n().predefined}>
                  <button
                    type="button"
                    class="btn btn-sm btn-ghost text-error ml-auto"
                    onClick={() => setRemoving(true)}
                  >
                    {t("actions.delete")}
                  </button>
                </Show>
              </div>
              <dl class="divide-y divide-base-200">
                <DetailRow label={t("networks.driver")}>{n().driver}</DetailRow>
                <DetailRow label={t("networks.scope")}>{n().scope}</DetailRow>
                <DetailRow label={t("networks.subnet")}>{n().subnet ?? "–"}</DetailRow>
                <DetailRow label={t("networks.gateway")}>{n().gateway ?? "–"}</DetailRow>
                <DetailRow label={t("networks.options")}>
                  <For
                    each={Object.entries(n().options)}
                    fallback={<span class="text-base-content/50">{t("detail.none")}</span>}
                  >
                    {([k, v]) => <div class="break-all font-mono text-xs">{`${k}=${v}`}</div>}
                  </For>
                </DetailRow>
              </dl>
              <h3 class="font-semibold">{t("networks.attached")}</h3>
              <Show
                when={n().attached.length > 0}
                fallback={<p class="text-sm text-base-content/70">{t("networks.noneAttached")}</p>}
              >
                <table class="table table-sm">
                  <thead>
                    <tr>
                      <th>{t("networks.name")}</th>
                      <th>{t("networks.ipv4")}</th>
                      <th>
                        <span class="sr-only">{t("containers.actions")}</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    <For each={n().attached}>
                      {(a) => (
                        <tr>
                          <td>
                            <button
                              type="button"
                              class="link link-hover"
                              onClick={() => navigate(`/containers/${a.id}`)}
                            >
                              {a.name}
                            </button>
                          </td>
                          <td class="font-mono text-xs">{a.ipv4 ?? ""}</td>
                          <td>
                            <div class="flex justify-end">
                              <IconButton
                                icon={IconPlugConnectedX}
                                label={`${t("networks.disconnect")} ${a.name}`}
                                tooltip={t("networks.disconnect")}
                                danger
                                disabled={busy()}
                                onClick={() => void disconnect(n(), a)}
                              />
                            </div>
                          </td>
                        </tr>
                      )}
                    </For>
                  </tbody>
                </table>
              </Show>
              <form
                class="flex flex-wrap items-end gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  void connect(n());
                }}
              >
                <Field label={t("networks.connectContainer")}>
                  <Select
                    class="select-sm w-56"
                    invalid={invalid("container")}
                    onChange={(e) => {
                      edited("container");
                      setChosen(e.currentTarget.value);
                    }}
                  >
                    <option value="" selected={chosen() === ""}>
                      –
                    </option>
                    <For each={connectable()}>
                      {(c) => (
                        <option value={c.id} selected={chosen() === c.id}>
                          {c.name}
                        </option>
                      )}
                    </For>
                  </Select>
                </Field>
                <Field label={t("networks.ipv4")}>
                  <Input
                    class="input-sm w-40 font-mono"
                    placeholder={t("networks.ipv4Hint")}
                    invalid={invalid("ipv4")}
                    value={ipv4()}
                    onInput={(e) => {
                      edited("ipv4");
                      setIpv4(e.currentTarget.value);
                    }}
                  />
                </Field>
                <button type="submit" class="btn btn-sm" disabled={busy() || !selected()}>
                  {t("networks.connect")}
                </button>
              </form>
              <Show when={containers.error}>
                <ErrorAlert error={errorText(containers.error, t)} onRetry={() => void refetchContainers()} />
              </Show>
            </>
          )}
        </Match>
      </Switch>
      <ConfirmDialog
        open={removing()}
        title={t("networks.deleteTitle")}
        message={t("networks.deleteMessage", { name: loaded()?.name ?? "" })}
        confirmLabel={t("actions.delete")}
        danger
        onConfirm={() => void remove()}
        onCancel={() => setRemoving(false)}
      />
    </section>
  );
}
