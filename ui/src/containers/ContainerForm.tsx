import { ErrorAlert, LoadingState, useApi, useExtensionNavigate, useI18n, useToast } from "@edelweiss/sdk";
import {
  createResource,
  createSignal,
  createUniqueId,
  For,
  Index,
  type JSX,
  Match,
  Show,
  Switch,
} from "solid-js";
import { createStore, unwrap } from "solid-js/store";
import type {
  ContainerInfo,
  CreateResult,
  EditResult,
  Mount,
  NetworkInfo,
  Policy,
  PullStart,
  Resources,
  Spec,
  Status,
  Volume,
} from "../api";
import { Input, Select } from "../components/Field";
import { errorField, errorText } from "../errors";
import { formatBytes } from "../format";
import { tPlural } from "../plural";
import { PullProgress } from "./PullProgress";
import { VolumeSelect } from "./VolumeSelect";

const policies: Policy[] = ["no", "always", "unless-stopped", "on-failure"];

const MiB = 2 ** 20;
const GiB = 2 ** 30;

function emptySpec(): Spec {
  return {
    name: "",
    image: "",
    command: "",
    ports: [],
    mounts: [],
    env: [],
    labels: [],
    network: "bridge",
    privileged: false,
    resources: { memory: 0, cpus: 0 },
    restart: { policy: "no", maxRetries: 0 },
  };
}

/** The CPU limit in whole nanoCPUs, as the backend compares it: 0.1 + 0.2 and 0.3 are the same limit. */
const nanoCPUs = (r: Resources) => Math.round(r.cpus * 1e9);

/**
 * Whether saving current instead of initial recreates the container (the backend's rule: anything but name, restart
 * policy and limits, and not the order of the ports, volumes, variables and labels, nor blanks around a value or a
 * label key; removing a limit too).
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
      labels: rows(s.labels.map((l) => ({ key: l.key.trim(), value: l.value }))),
    });
  const removed =
    (initial.resources.memory > 0 && current.resources.memory === 0) ||
    (nanoCPUs(initial.resources) > 0 && nanoCPUs(current.resources) === 0);
  return removed || key(initial) !== key(current);
}

/** The JSON for the backend: without the view-only flag of anonymous volumes, the label keys without blanks around them. */
function payload(s: Spec): Spec {
  const copy = structuredClone(unwrap(s));
  copy.mounts = copy.mounts.map(({ anonymous: _anonymous, ...m }) => m);
  copy.labels = copy.labels.map((l) => ({ key: l.key.trim(), value: l.value }));
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
  const i18n = useI18n();
  const { t, locale } = i18n;
  const toast = useToast();
  const navigate = useExtensionNavigate();
  const [spec, setSpec] = createStore<Spec>(structuredClone(props.initial));
  const [startNow, setStartNow] = createSignal(true);
  const [pullAlways, setPullAlways] = createSignal(false);
  const [pulling, setPulling] = createSignal<PullStart>();
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const [volumes] = createResource(() => api.get<Volume[]>("/volumes"));
  const [networks] = createResource(() =>
    api.get<NetworkInfo[]>("/networks").catch(() => [] as NetworkInfo[]),
  );
  // the host's CPUs and memory, for the hints of the limits; without them the hints name no maximum
  const [host] = createResource(() => api.get<Status>("/status").catch(() => undefined));
  // The hints wait for the answer: one that gains its maximum a moment later would flash. A failed request is an
  // answer too (the hints without maximum).
  const hostAnswered = () => host.state === "ready";
  const recreates = () => props.mode === "edit" && needsRecreate(props.initial, spec);

  // The memory limit is typed as a number with a unit; the form keeps the bytes. The unit is GiB when the stored limit
  // is a whole number of GiB, else MiB (a limit of 1536 MiB reads 1536, not 1.5).
  const initialMemory = props.initial.resources.memory;
  const [unit, setUnit] = createSignal<"MiB" | "GiB">(
    initialMemory > 0 && initialMemory % GiB === 0 ? "GiB" : "MiB",
  );
  const initialMemoryText = initialMemory > 0 ? String(initialMemory / (unit() === "GiB" ? GiB : MiB)) : "";
  // The fields are not written back while the user types: a number field of a browser reports "" for a half-typed
  // "1." and clears itself when a new value is assigned. They show their first value and keep what is typed.
  let memoryText = initialMemoryText;
  const initialCpus = props.initial.resources.cpus;
  const applyMemory = () => {
    const n = Number(memoryText.replace(",", "."));
    if (memoryText.trim() === "" || !(n > 0)) return setSpec("resources", "memory", 0);
    // What the backend refuses must reach it, not turn into no limit on the way: at least one byte (a smaller number
    // rounds to 0), and at most the largest safe integer (JSON writes Infinity as null, and a number like 1e+30 is no
    // integer for the backend); that is far above its maximum of 1 PiB, so it answers with an error of the field.
    const bytes = Math.max(1, Math.round(n * (unit() === "GiB" ? GiB : MiB)));
    setSpec("resources", "memory", Math.min(bytes, Number.MAX_SAFE_INTEGER));
  };
  // Advice only, nothing is blocked: the backend's own maximum is far above any host (1 PiB), and a limit above the
  // memory of this host may have its reason (a host that grows, swap).
  const memoryOverHost = () => {
    const max = host()?.memory ?? 0;
    return max > 0 && spec.resources.memory > max;
  };
  // the backend names the rows of this form (ports.1.hostPort is the second port), in the order they were sent
  const invalid = (field: string) => {
    const f = errorField(error());
    return f === field || (f?.startsWith(`${field}.`) ?? false);
  };

  const save = async () => {
    try {
      if (props.mode === "create") {
        const res = await api.post<CreateResult>("/containers", { ...payload(spec), start: startNow() });
        for (const w of res.warnings ?? []) toast.info(w);
        if (res.warning) toast.info(res.warning);
        else toast.success(t("form.created", { name: spec.name }));
        navigate(`/containers/${res.id}`);
      } else {
        const res = await api.put<EditResult>(
          `/containers/${encodeURIComponent(props.id ?? "")}`,
          payload(spec),
        );
        for (const w of res.warnings ?? []) toast.info(w);
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
        {/* For, not Index: a row keeps its own state (VolumeSelect's "new volume") when another row is removed */}
        <For each={spec.mounts}>
          {(m, i) => {
            // the radio group of the row must not change with its index
            const group = createUniqueId();
            return (
              <div class="flex flex-wrap items-end gap-2 border-b border-base-200 pb-2" data-mount>
                <div class="join">
                  <label
                    class="join-item btn btn-sm"
                    classList={{ "btn-active": m.type === "volume", "btn-disabled": !!m.anonymous }}
                  >
                    <input
                      type="radio"
                      class="sr-only"
                      name={`mount-type-${group}`}
                      checked={m.type === "volume"}
                      disabled={m.anonymous}
                      aria-label={t("form.mountVolume")}
                      onChange={() => setMount(i(), { type: "volume", source: "", anonymous: false })}
                    />
                    {t("form.mountVolume")}
                  </label>
                  <label
                    class="join-item btn btn-sm"
                    classList={{ "btn-active": m.type === "bind", "btn-disabled": !!m.anonymous }}
                  >
                    <input
                      type="radio"
                      class="sr-only"
                      name={`mount-type-${group}`}
                      checked={m.type === "bind"}
                      disabled={m.anonymous}
                      aria-label={t("form.mountBind")}
                      onChange={() => setMount(i(), { type: "bind", source: "", anonymous: false })}
                    />
                    {t("form.mountBind")}
                  </label>
                </div>
                <Show
                  when={m.type === "volume"}
                  fallback={
                    <label class="flex flex-col gap-1">
                      <span class="text-xs">{t("form.hostPath")}</span>
                      <Input
                        class="input-sm w-56 font-mono"
                        placeholder="/srv/data"
                        invalid={invalid(`mounts.${i()}.source`)}
                        value={m.source}
                        onInput={(e) => setMount(i(), { source: e.currentTarget.value })}
                      />
                    </label>
                  }
                >
                  <Show
                    when={!m.anonymous}
                    fallback={
                      <>
                        <label class="flex flex-col gap-1">
                          <span class="text-xs">{t("form.volumeName")}</span>
                          <Input
                            class="input-sm w-56"
                            readOnly
                            title={m.source}
                            value={m.source.slice(0, 12)}
                          />
                        </label>
                        <span class="badge badge-ghost badge-sm">{t("detail.anonymous")}</span>
                      </>
                    }
                  >
                    <VolumeSelect
                      value={m.source}
                      volumes={volumes.error ? undefined : volumes()}
                      failed={volumes.error}
                      invalid={invalid(`mounts.${i()}.source`)}
                      onChange={(name) => setMount(i(), { source: name })}
                    />
                  </Show>
                </Show>
                <label class="flex flex-col gap-1">
                  <span class="text-xs">{t("form.target")}</span>
                  <Input
                    class="input-sm w-56 font-mono"
                    placeholder="/data"
                    invalid={invalid(`mounts.${i()}.target`)}
                    value={m.target}
                    onInput={(e) => setMount(i(), { target: e.currentTarget.value })}
                  />
                </label>
                <label class="flex items-center gap-1">
                  <input
                    type="checkbox"
                    class="checkbox checkbox-sm"
                    checked={m.readOnly}
                    onChange={(e) => setMount(i(), { readOnly: e.currentTarget.checked })}
                  />
                  <span class="text-xs">{t("form.readOnly")}</span>
                </label>
                <button
                  type="button"
                  class="btn btn-sm btn-ghost"
                  onClick={() => setSpec("mounts", (ms) => ms.filter((_, j) => j !== i()))}
                >
                  {t("actions.remove")}
                </button>
              </div>
            );
          }}
        </For>
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

      <Section title={t("form.labels")}>
        <Index each={spec.labels}>
          {(l, i) => (
            <div class="flex flex-wrap items-end gap-2" data-label>
              <label class="flex flex-col gap-1">
                <span class="text-xs">{t("form.labelKey")}</span>
                <Input
                  class="input-sm w-48 font-mono"
                  invalid={invalid(`labels.${i}.key`)}
                  value={l().key}
                  onInput={(e) => setSpec("labels", i, "key", e.currentTarget.value)}
                />
              </label>
              <label class="flex flex-1 flex-col gap-1">
                <span class="text-xs">{t("form.labelValue")}</span>
                <input
                  class="input input-sm font-mono"
                  value={l().value}
                  onInput={(e) => setSpec("labels", i, "value", e.currentTarget.value)}
                />
              </label>
              <button
                type="button"
                class="btn btn-sm btn-ghost"
                onClick={() => setSpec("labels", (ls) => ls.filter((_, j) => j !== i))}
              >
                {t("actions.remove")}
              </button>
            </div>
          )}
        </Index>
        <button
          type="button"
          class="btn btn-sm btn-ghost self-start"
          onClick={() => setSpec("labels", (ls) => [...ls, { key: "", value: "" }])}
        >
          {t("form.addLabel")}
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

      <Section title={t("form.resources")}>
        <div class="flex flex-wrap items-end gap-2">
          <label class="flex flex-col gap-1">
            <span class="text-xs">{t("form.memoryLimit")}</span>
            {/* step any: the number may have decimals (1.5 GiB), and a stored limit may be no whole MiB */}
            <Input
              class="input-sm w-32"
              type="number"
              min="0"
              step="any"
              aria-describedby="docker-memory-hint"
              invalid={invalid("resources.memory")}
              value={initialMemoryText}
              onInput={(e) => {
                memoryText = e.currentTarget.value;
                applyMemory();
              }}
            />
          </label>
          <label class="flex flex-col gap-1">
            <span class="text-xs">{t("form.unit")}</span>
            <Select
              class="select-sm w-24"
              value={unit()}
              onChange={(e) => {
                setUnit(e.currentTarget.value as "MiB" | "GiB");
                applyMemory();
              }}
            >
              <option value="MiB" selected={unit() === "MiB"}>
                MiB
              </option>
              <option value="GiB" selected={unit() === "GiB"}>
                GiB
              </option>
            </Select>
          </label>
          <span
            id="docker-memory-hint"
            class="pb-2 text-xs"
            classList={{ "text-warning": memoryOverHost(), "text-base-content/70": !memoryOverHost() }}
          >
            <Show when={hostAnswered()}>
              {host()?.memory
                ? t("form.memoryHint", { size: formatBytes(host()?.memory ?? 0, locale()) })
                : t("form.memoryHintNoMax")}
            </Show>
          </span>
        </div>
        <div class="flex flex-wrap items-end gap-2">
          <label class="flex flex-col gap-1">
            <span class="text-xs">{t("form.cpuLimit")}</span>
            {/* min is the base of the steps: 0.01 with step 0.1 would make 0.5 or 1.5 a step a browser refuses */}
            <Input
              class="input-sm w-32"
              type="number"
              min="0.01"
              step="any"
              aria-describedby="docker-cpu-hint"
              invalid={invalid("resources.cpus")}
              value={initialCpus > 0 ? String(initialCpus) : ""}
              onInput={(e) => {
                const n = Number(e.currentTarget.value.replace(",", "."));
                setSpec("resources", "cpus", n > 0 ? n : 0);
              }}
            />
          </label>
          <span id="docker-cpu-hint" class="pb-2 text-xs text-base-content/70">
            <Show when={hostAnswered()}>
              {host()?.cpus ? tPlural(i18n, "form.cpuHint", host()?.cpus ?? 0) : t("form.cpuHintNoMax")}
            </Show>
          </span>
        </div>
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
