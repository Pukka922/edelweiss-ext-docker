import { useApi, useI18n, useToast } from "@edelweiss/sdk";
import { createSignal } from "solid-js";
import { shortId, type Volume } from "../api";
import { Input } from "../components/Field";
import { Field, FormDialog } from "../components/FormDialog";
import { KeyValueRows, type KV, toMap } from "../components/KeyValueRows";
import { errorField, errorText } from "../errors";

export function VolumeDialog(props: { onClose: (created: boolean) => void }) {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  const [name, setName] = createSignal("");
  const [driver, setDriver] = createSignal("local");
  const [options, setOptions] = createSignal<KV[]>([]);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const invalid = (field: string) => errorField(error()) === field;

  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const v = await api.post<Volume>("/volumes", {
        name: name().trim(),
        driver: driver().trim(),
        options: toMap(options()),
      });
      // without a name Docker picks a 64-character one, which the list shows as its first 12
      toast.success(t("volumes.createdToast", { name: v.anonymous ? shortId(v.name) : v.name }));
      props.onClose(true);
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  };

  return (
    <FormDialog
      title={t("volumes.create")}
      submitLabel={t("actions.create")}
      busy={busy()}
      error={error() ? errorText(error(), t) : undefined}
      onSubmit={() => void submit()}
      onCancel={() => props.onClose(false)}
    >
      <Field label={t("volumes.name")}>
        <Input
          autofocus
          placeholder={t("volumes.nameHint")}
          invalid={invalid("name")}
          value={name()}
          onInput={(e) => setName(e.currentTarget.value)}
        />
      </Field>
      <Field label={t("volumes.driver")}>
        <Input
          invalid={invalid("driver")}
          value={driver()}
          onInput={(e) => setDriver(e.currentTarget.value)}
        />
      </Field>
      <KeyValueRows label={t("volumes.options")} rows={options()} onChange={setOptions} />
      <p class="text-xs text-base-content/70">{t("volumes.optionsHint")}</p>
    </FormDialog>
  );
}
