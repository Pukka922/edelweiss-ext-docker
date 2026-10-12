import { useApi, useI18n, useToast } from "@edelweiss/sdk";
import { createSignal, For } from "solid-js";
import { Input, Select } from "../components/Field";
import { Field, FormDialog } from "../components/FormDialog";
import { KeyValueRows, type KV, toMap } from "../components/KeyValueRows";
import { errorField, errorText } from "../errors";

const drivers = ["bridge", "macvlan", "ipvlan"];

export function NetworkDialog(props: { onClose: (created: boolean) => void }) {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  const [name, setName] = createSignal("");
  const [driver, setDriver] = createSignal("bridge");
  const [subnet, setSubnet] = createSignal("");
  const [gateway, setGateway] = createSignal("");
  const [internal, setInternal] = createSignal(false);
  const [options, setOptions] = createSignal<KV[]>([]);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const invalid = (field: string) => errorField(error()) === field;

  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await api.post("/networks", {
        name: name().trim(),
        driver: driver(),
        subnet: subnet().trim(),
        gateway: gateway().trim(),
        internal: internal(),
        options: toMap(options()),
      });
      toast.success(t("networks.createdToast", { name: name().trim() }));
      props.onClose(true);
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  };

  return (
    <FormDialog
      title={t("networks.create")}
      submitLabel={t("actions.create")}
      busy={busy()}
      error={error() ? errorText(error(), t) : undefined}
      onSubmit={() => void submit()}
      onCancel={() => props.onClose(false)}
    >
      <Field label={t("networks.name")}>
        <Input
          required
          autofocus
          invalid={invalid("name")}
          value={name()}
          onInput={(e) => setName(e.currentTarget.value)}
        />
      </Field>
      <Field label={t("networks.driver")}>
        <Select invalid={invalid("driver")} onChange={(e) => setDriver(e.currentTarget.value)}>
          <For each={drivers}>
            {(d) => (
              <option value={d} selected={driver() === d}>
                {d}
              </option>
            )}
          </For>
        </Select>
      </Field>
      <Field label={t("networks.subnet")}>
        <Input
          class="font-mono"
          placeholder="172.30.0.0/24"
          invalid={invalid("subnet")}
          value={subnet()}
          onInput={(e) => setSubnet(e.currentTarget.value)}
        />
      </Field>
      <Field label={t("networks.gateway")}>
        <Input
          class="font-mono"
          placeholder="172.30.0.1"
          invalid={invalid("gateway")}
          value={gateway()}
          onInput={(e) => setGateway(e.currentTarget.value)}
        />
      </Field>
      <label class="flex items-center gap-2">
        <input
          type="checkbox"
          class="checkbox checkbox-sm"
          checked={internal()}
          onChange={(e) => setInternal(e.currentTarget.checked)}
        />
        <span class="text-sm">{t("networks.internal")}</span>
      </label>
      <KeyValueRows label={t("networks.options")} rows={options()} onChange={setOptions} />
      <p class="text-xs text-base-content/70">{t("networks.optionsHint")}</p>
    </FormDialog>
  );
}
