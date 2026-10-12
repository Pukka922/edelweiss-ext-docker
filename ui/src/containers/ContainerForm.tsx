import { ErrorAlert, LoadingState, useApi, useExtensionNavigate, useI18n, useToast } from "@edelweiss/sdk";
import { createResource, createSignal, For, Index, type JSX, Match, Show, Switch } from "solid-js";
import { createStore, unwrap } from "solid-js/store";
import type {
  ContainerInfo,
  CreateResult,
  EditResult,
  Mount,
  NetworkInfo,
  Policy,
  PullStart,
  Spec,
  Volume,
} from "../api";
import { Input, Select } from "../components/Field";
import { errorField, errorText } from "../errors";
import { PullProgress } from "./PullProgress";

const policies: Policy[] = ["no", "always", "unless-stopped", "on-failure"];

function emptySpec(): Spec {
  return {
    name: "",
    image: "",
    command: "",
    ports: [],
    mounts: [],
    env: [],
    network: "bridge",
    privileged: false,
    restart: { policy: "no", maxRetries: 0 },
  };
}

/**
 * Whether saving current instead of initial recreates the container (the backend's rule: anything but name and
 * restart policy, and not the order of the ports, volumes and variables, nor blanks around a value).
 */
export function needsRecreate(initial: Spec, current: Spec): boolean {
  // each row as JSON with its keys in a fixed order, the rows sorted: equal sets of rows give equal lists
  const rows = <T,>(list: T[]) => list.map((row) => JSON.stringify(row)).sort();
  const key = (s: Spec) =>
    JSON.stringify({
      image: s.image.trim(),
      command: s.command.trim(),
      network: s.network,
      privileged: s.privileged,
      ports: rows(
        s.ports.map((p) => ({
          hostIp: p.hostIp.trim(),
          hostPort: p.hostPort,
          containerPort: p.containerPort,
          protocol: p.protocol,
        })),
      ),
      mounts: rows(
        s.mounts.map((m) => ({
          type: m.type,
          source: m.source.trim(),
          target: m.target.trim(),
          readOnly: m.readOnly,
        })),
      ),
      env: rows(s.env.map((e) => ({ name: e.name.trim(), value: e.value }))),
    });
  return key(initial) !== key(current);
}

/** The JSON for the backend: without the view-only flag of anonymous volumes. */
function payload(s: Spec): Spec {
  const copy = structuredClone(unwrap(s));
  copy.mounts = copy.mounts.map(({ anonymous: _anonymous, ...m }) => m);
  return copy;
}

function Section(props: { title: string; children: JSX.Element }) {
  return (
    <fieldset class="fieldset rounded-box border border-base-300 p-4">
      <legend class="fieldset-legend">{props.title}</legend>
      {props.children}
    </fieldset>
  );
}

function Form(props: { mode: "create" | "edit"; initial: Spec; id?: string }) {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  const navigate = useExtensionNavigate();
  const [spec, setSpec] = createStore<Spec>(structuredClone(props.initial));
  const [startNow, setStartNow] = createSignal(true);
  const [pullAlways, setPullAlways] = createSignal(false);
  const [pulling, setPulling] = createSignal<PullStart>();
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const [volumes] = createResource(() => api.get<Volume[]>("/volumes").catch(() => [] as Volume[]));
  const [networks] = createResource(() =>
    api.get<NetworkInfo[]>("/networks").catch(() => [] as NetworkInfo[]),
  );
  const recreates = () => props.mode === "edit" && needsRecreate(props.initial, spec);
  // the backend names the rows of this form (ports.1.hostPort is the second port), in the order they were sent
  const invalid = (field: string) => {
    const f = errorField(error());
    return f === field || (f?.startsWith(`${field}.`) ?? false);
  };
  const knownVolume = (name: string) => (volumes() ?? []).some((v) => v.name === name);

  const save = async () => {
    try {
      if (props.mode === "create") {
        const res = await api.post<CreateResult>("/containers", { ...payload(spec), start: startNow() });
        if (res.warning) toast.info(res.warning);
        else toast.success(t("form.created", { name: spec.name }));
        navigate(`/containers/${res.id}`);
      } else {
        const res = await api.put<EditResult>(
          `/containers/${encodeURIComponent(props.id ?? "")}`,
          payload(spec),
        );
        if (res.warning) toast.info(res.warning);
        else toast.success(t(res.recreated ? "form.recreated" : "form.saved", { name: spec.name }));
        navigate(`/containers/${res.id}`);
      }
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  const submit = async (e: Event) => {
    e.preventDefault();
    if (busy()) return;
    setBusy(true);
    setError(undefined);
    try {
      const present =
        !pullAlways() &&
        (await api.get<{ present: boolean }>(`/images/present?ref=${encodeURIComponent(spec.image.trim())}`))
          .present;
      if (!present) {
        setPulling(await api.post<PullStart>("/pulls", { ref: spec.image.trim() }));
        return; // save() runs when the pull is done
      }
      await save();
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  };

  const setMount = (i: number, patch: Partial<Mount>) => setSpec("mounts", i, patch);

  return (
    <form class="flex flex-col gap-4" onSubmit={(e) => void submit(e)}>
      <Section title={t("form.general")}>
        <label class="flex flex-col gap-1">
          <span class="text-sm">{t("form.name")}</span>
          <Input
            required
            invalid={invalid("name")}
            value={spec.name}
            onInput={(e) => setSpec("name", e.currentTarget.value)}
          />
        </label>
        <label class="flex flex-col gap-1">
          <span class="text-sm">{t("form.image")}</span>
          <Input
            required
            placeholder={t("form.imageHint")}
            invalid={invalid("image")}
            value={spec.image}
            onInput={(e) => setSpec("image", e.currentTarget.value)}
          />
        </label>
        <label class="flex items-center gap-2">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked={pullAlways()}
            onChange={(e) => setPullAlways(e.currentTarget.checked)}
          />
          <span class="text-sm">{t("form.pullAlways")}</span>
        </label>
        <Show when={props.mode === "edit" && pullAlways()}>
          <p class="text-sm text-base-content/70">{t("form.reloadMayRecreate")}</p>
        </Show>
        <label class="flex flex-col gap-1">
          <span class="text-sm">{t("form.command")}</span>
          <Input
            class="font-mono"
            placeholder={t("form.commandHint")}
            invalid={invalid("command")}
            value={spec.command}
            onInput={(e) => setSpec("command", e.currentTarget.value)}
          />
        </label>
        <Show when={props.mode === "create"}>
          <label class="flex items-center gap-2">
            <input
              type="checkbox"
              class="checkbox checkbox-sm"
              checked={startNow()}
              onChange={(e) => setStartNow(e.currentTarget.checked)}
            />
            <span class="text-sm">{t("form.startNow")}</span>
          </label>
        </Show>
      </Section>

      <Section title={t("form.ports")}>
        <Index each={spec.ports}>
          {(p, i) => (
            <div class="flex flex-wrap items-end gap-2" data-port>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.hostIp")}</span>
                <Input
                  class="input-sm w-36"
                  placeholder={t("form.hostIpHint")}
                  invalid={invalid(`ports.${i}.hostIp`)}
                  value={p().hostIp}
                  onInput={(e) => setSpec("ports", i, "hostIp", e.currentTarget.value)}
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.hostPort")}</span>
                <Input
                  class="input-sm w-28"
                  type="number"
                  min="0"
                  max="65535"
                  placeholder={t("form.hostPortHint")}
                  aria-describedby="docker-host-port-hint"
                  invalid={invalid(`ports.${i}.hostPort`)}
                  value={p().hostPort || ""}
                  onInput={(e) => setSpec("ports", i, "hostPort", Number(e.currentTarget.value) || 0)}
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.containerPort")}</span>
                <Input
                  class="input-sm w-28"
                  type="number"
                  min="1"
                  max="65535"
                  required
                  invalid={invalid(`ports.${i}.containerPort`)}
                  value={p().containerPort || ""}
                  onInput={(e) => setSpec("ports", i, "containerPort", Number(e.currentTarget.value) || 0)}
                />
              </label>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.protocol")}</span>
                <Select
                  class="select-sm w-24"
                  invalid={invalid(`ports.${i}.protocol`)}
                  value={p().protocol}
                  onChange={(e) => setSpec("ports", i, "protocol", e.currentTarget.value as "tcp" | "udp")}
                >
                  <option value="tcp">tcp</option>
                  <option value="udp">udp</option>
                </Select>
              </label>
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                onClick={() => setSpec("ports", (ps) => ps.filter((_, j) => j !== i))}
              >
                {t("actions.remove")}
              </button>
            </div>
          )}
        </Index>
        <Show when={spec.ports.length > 0}>
          <p id="docker-host-port-hint" class="text-xs text-base-content/70">
            {t("form.hostPortRangeHint")}
          </p>
        </Show>
        <button
          type="button"
          class="btn btn-sm btn-ghost self-start"
          onClick={() =>
            setSpec("ports", (ps) => [...ps, { hostIp: "", hostPort: 0, containerPort: 0, protocol: "tcp" }])
          }
        >
          {t("form.addPort")}
        </button>
      </Section>

      <Section title={t("form.mounts")}>
        <datalist id="docker-volume-names">
          <For each={(volumes() ?? []).filter((v) => !v.anonymous)}>
            {(v) => <option value={v.name}>{v.driver}</option>}
          </For>
        </datalist>
        <Index each={spec.mounts}>
          {(m, i) => (
            <div class="flex flex-wrap items-end gap-2 border-b border-base-200 pb-2" data-mount>
              <div class="join">
                <label
                  class="join-item btn btn-sm"
                  classList={{ "btn-active": m().type === "volume", "btn-disabled": !!m().anonymous }}
                >
                  <input
                    type="radio"
                    class="sr-only"
                    name={`mount-type-${i}`}
                    checked={m().type === "volume"}
                    disabled={m().anonymous}
                    aria-label={t("form.mountVolume")}
                    onChange={() => setMount(i, { type: "volume", source: "", anonymous: false })}
                  />
                  {t("form.mountVolume")}
                </label>
                <label
                  class="join-item btn btn-sm"
                  classList={{ "btn-active": m().type === "bind", "btn-disabled": !!m().anonymous }}
                >
                  <input
                    type="radio"
                    class="sr-only"
                    name={`mount-type-${i}`}
                    checked={m().type === "bind"}
                    disabled={m().anonymous}
                    aria-label={t("form.mountBind")}
                    onChange={() => setMount(i, { type: "bind", source: "", anonymous: false })}
                  />
                  {t("form.mountBind")}
                </label>
              </div>
              <Show
                when={m().type === "volume"}
                fallback={
                  <label class="flex flex-col gap-1">
                    <span class="text-xs">{t("form.hostPath")}</span>
                    <Input
                      class="input-sm w-56 font-mono"
                      placeholder="/srv/data"
                      invalid={invalid(`mounts.${i}.source`)}
                      value={m().source}
                      onInput={(e) => setMount(i, { source: e.currentTarget.value })}
                    />
                  </label>
                }
              >
                <label class="flex flex-col gap-1">
                  <span class="text-xs">{t("form.volumeName")}</span>
                  <Input
                    class="input-sm w-56"
                    list="docker-volume-names"
                    placeholder={t("form.volumeHint")}
                    readOnly={m().anonymous}
                    title={m().anonymous ? m().source : undefined}
                    invalid={invalid(`mounts.${i}.source`)}
                    value={m().anonymous ? m().source.slice(0, 12) : m().source}
                    onInput={(e) => setMount(i, { source: e.currentTarget.value })}
                  />
                </label>
                <Show when={m().anonymous}>
                  <span class="badge badge-ghost badge-sm">{t("detail.anonymous")}</span>
                </Show>
                <Show
                  when={
                    !m().anonymous && m().source.trim() !== "" && volumes() && !knownVolume(m().source.trim())
                  }
                >
                  <span class="badge badge-info badge-sm">{t("form.newVolume")}</span>
                </Show>
              </Show>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.target")}</span>
                <Input
                  class="input-sm w-56 font-mono"
                  placeholder="/data"
                  invalid={invalid(`mounts.${i}.target`)}
                  value={m().target}
                  onInput={(e) => setMount(i, { target: e.currentTarget.value })}
                />
              </label>
              <label class="flex items-center gap-1">
                <input
                  type="checkbox"
                  class="checkbox checkbox-sm"
                  checked={m().readOnly}
                  onChange={(e) => setMount(i, { readOnly: e.currentTarget.checked })}
                />
                <span class="text-xs">{t("form.readOnly")}</span>
              </label>
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                onClick={() => setSpec("mounts", (ms) => ms.filter((_, j) => j !== i))}
              >
                {t("actions.remove")}
              </button>
            </div>
          )}
        </Index>
        <button
          type="button"
          class="btn btn-sm btn-ghost self-start"
          onClick={() =>
            setSpec("mounts", (ms) => [...ms, { type: "volume", source: "", target: "", readOnly: false }])
          }
        >
          {t("form.addMount")}
        </button>
      </Section>

      <Section title={t("form.env")}>
        <Index each={spec.env}>
          {(v, i) => (
            <div class="flex flex-wrap items-end gap-2" data-env>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.envName")}</span>
                <Input
                  class="input-sm w-48 font-mono"
                  invalid={invalid(`env.${i}.name`)}
                  value={v().name}
                  onInput={(e) => setSpec("env", i, "name", e.currentTarget.value)}
                />
              </label>
              <label class="flex flex-1 flex-col gap-1">
                <span class="text-xs">{t("form.envValue")}</span>
                <input
                  class="input input-sm font-mono"
                  value={v().value}
                  onInput={(e) => setSpec("env", i, "value", e.currentTarget.value)}
                />
              </label>
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                onClick={() => setSpec("env", (es) => es.filter((_, j) => j !== i))}
              >
                {t("actions.remove")}
              </button>
            </div>
          )}
        </Index>
        <button
          type="button"
          class="btn btn-sm btn-ghost self-start"
          onClick={() => setSpec("env", (es) => [...es, { name: "", value: "" }])}
        >
          {t("form.addEnv")}
        </button>
      </Section>

      <Section title={t("form.network")}>
        <label class="flex flex-col gap-1">
          <span class="text-sm">{t("form.network")}</span>
          {/* selected per option: the list arrives after the select, and select.value set earlier would be lost */}
          <Select invalid={invalid("network")} onChange={(e) => setSpec("network", e.currentTarget.value)}>
            <Show when={!(networks() ?? []).some((n) => n.name === spec.network)}>
              <option value={spec.network} selected>
                {spec.network}
              </option>
            </Show>
            <For each={networks() ?? []}>
              {(n) => (
                <option value={n.name} selected={n.name === spec.network}>
                  {n.name}
                </option>
              )}
            </For>
          </Select>
        </label>
      </Section>

      <Section title={t("form.restart")}>
        <label class="flex flex-col gap-1">
          <span class="text-sm">{t("form.restart")}</span>
          <Select
            invalid={invalid("restart.policy")}
            value={spec.restart.policy}
            onChange={(e) => setSpec("restart", { policy: e.currentTarget.value as Policy, maxRetries: 0 })}
          >
            <For each={policies}>{(p) => <option value={p}>{t(`form.policies.${p}`)}</option>}</For>
          </Select>
        </label>
        <Show when={spec.restart.policy === "on-failure"}>
          <label class="flex flex-col gap-1">
            <span class="text-sm">{t("form.maxRetries")}</span>
            <Input
              class="w-28"
              type="number"
              min="0"
              invalid={invalid("restart.maxRetries")}
              value={spec.restart.maxRetries}
              onInput={(e) => setSpec("restart", "maxRetries", Number(e.currentTarget.value) || 0)}
            />
          </label>
        </Show>
      </Section>

      <Section title={t("form.advanced")}>
        <label class="flex items-center gap-2">
          <input
            type="checkbox"
            class="checkbox checkbox-sm"
            checked={spec.privileged}
            onChange={(e) => setSpec("privileged", e.currentTarget.checked)}
          />
          <span class="text-sm">{t("form.privileged")}</span>
        </label>
        <Show when={spec.privileged}>
          <p class="text-sm text-warning">{t("form.privilegedWarning")}</p>
        </Show>
      </Section>

      <Show when={recreates()}>
        <div role="status" class="alert alert-info">
          {t("form.recreateNotice")}
        </div>
      </Show>
      <Show when={error()}>
        {(err) => (
          <div role="alert" class="alert alert-error">
            {errorText(err(), t)}
          </div>
        )}
      </Show>
      <Show when={recreates()}>
        <p role="status" class="text-right text-sm text-base-content/70">
          {t("form.recreateStopHint")}
        </p>
      </Show>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="btn"
          onClick={() => navigate(props.id ? `/containers/${props.id}` : "/containers")}
        >
          {t("actions.cancel")}
        </button>
        <button type="submit" class="btn btn-primary" disabled={busy()}>
          <Show when={busy()}>
            <span class="loading loading-spinner loading-sm" aria-hidden="true" />
          </Show>
          {props.mode === "create" ? t("form.submitCreate") : t("form.submitEdit")}
        </button>
      </div>
      <Show when={pulling()} keyed>
        {(job) => (
          <PullProgress
            id={job.id}
            image={job.ref}
            onDone={() => {
              setPulling(undefined);
              void save();
            }}
            onError={(message) => {
              setPulling(undefined);
              setBusy(false);
              setError(new Error(t("pull.failed", { message })));
            }}
            onClose={() => {
              setPulling(undefined);
              setBusy(false);
            }}
          />
        )}
      </Show>
    </form>
  );
}

export function ContainerCreate() {
  const { t } = useI18n();
  return (
    <section class="flex flex-col gap-3">
      <h2 class="text-xl font-semibold">{t("form.createTitle")}</h2>
      <Form mode="create" initial={emptySpec()} />
    </section>
  );
}

export function ContainerEdit(props: { params: Record<string, string> }) {
  const api = useApi();
  const { t } = useI18n();
  const [info, { refetch }] = createResource(
    () => props.params.id ?? "",
    (id) => api.get<ContainerInfo>(`/containers/${encodeURIComponent(id)}`),
  );
  return (
    <section class="flex flex-col gap-3">
      <Switch fallback={<LoadingState />}>
        <Match when={info.error}>
          <ErrorAlert error={errorText(info.error, t)} onRetry={() => void refetch()} />
        </Match>
        <Match when={info()} keyed>
          {(i) => (
            <>
              <h2 class="text-xl font-semibold">{t("form.editTitle", { name: i.container.name })}</h2>
              <Form mode="edit" initial={i.spec} id={i.container.id} />
            </>
          )}
        </Match>
      </Switch>
    </section>
  );
}
