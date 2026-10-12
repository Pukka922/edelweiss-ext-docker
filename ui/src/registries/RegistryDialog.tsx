import { useApi, useI18n, useToast } from "@edelweiss/sdk";
import { createEffect, createSignal, on, Show } from "solid-js";
import { createStore } from "solid-js/store";
import type { Registry, RegistryInput, TestResult } from "../api";
import { Input } from "../components/Field";
import { Field, FormDialog } from "../components/FormDialog";
import { errorField, errorText } from "../errors";

/** Adds a registry (without entry) or edits one; an empty password keeps the stored one. */
export function RegistryDialog(props: { entry?: Registry; onClose: (saved: boolean) => void }) {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  const [form, setForm] = createStore<RegistryInput>({
    name: props.entry?.name ?? "",
    address: props.entry?.address ?? "",
    auth: (props.entry?.username ?? "") !== "",
    username: props.entry?.username ?? "",
    password: "",
  });
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const [tested, setTested] = createSignal<TestResult>();
  const [testing, setTesting] = createSignal(false);
  const invalid = (field: string) => errorField(error()) === field;
  const input = (): RegistryInput => ({ ...form });

  const save = async () => {
    setBusy(true);
    setError(undefined);
    try {
      if (props.entry) {
        await api.put(`/registries/${encodeURIComponent(props.entry.id)}`, input());
        toast.success(t("registries.saved", { name: form.name.trim() }));
      } else {
        await api.post("/registries", input());
        toast.success(t("registries.added", { name: form.name.trim() }));
      }
      props.onClose(true);
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  };
  // The result of a test belongs to the values it was made with: any change of a field removes the banner, and the
  // result of a test that was running meanwhile is dropped.
  let edits = 0;
  createEffect(
    on(
      () => [form.name, form.address, form.auth, form.username, form.password],
      () => {
        edits++;
        setTested(undefined);
      },
      { defer: true },
    ),
  );
  const test = async () => {
    const seen = edits;
    setTested(undefined);
    setError(undefined);
    setTesting(true);
    try {
      const res = await api.post<TestResult>("/registries/test", {
        id: props.entry?.id ?? "",
        address: form.address,
        auth: form.auth,
        username: form.username,
        password: form.password,
      });
      if (seen === edits) setTested(res);
    } catch (err) {
      setError(err);
    } finally {
      setTesting(false);
    }
  };

  return (
    <FormDialog
      title={props.entry ? t("registries.editTitle") : t("registries.addTitle")}
      submitLabel={props.entry ? t("actions.save") : t("actions.add")}
      busy={busy()}
      error={error() ? errorText(error(), t) : undefined}
      extraActions={
        <button
          type="button"
          class="btn"
          disabled={testing() || form.address.trim() === ""}
          onClick={() => void test()}
        >
          {t("registries.test")}
        </button>
      }
      onSubmit={() => void save()}
      onCancel={() => props.onClose(false)}
    >
      <Field label={t("registries.name")}>
        <Input
          required
          maxLength={64}
          invalid={invalid("name")}
          value={form.name}
          onInput={(e) => setForm("name", e.currentTarget.value)}
        />
      </Field>
      <Field label={t("registries.address")}>
        <Input
          required
          placeholder={t("registries.addressHint")}
          invalid={invalid("address")}
          value={form.address}
          onInput={(e) => setForm("address", e.currentTarget.value)}
        />
      </Field>
      <Show when={form.address.trim().toLowerCase().startsWith("http://")}>
        <p class="text-xs text-base-content/70">{t("registries.httpHint")}</p>
      </Show>
      <fieldset class="flex flex-col gap-1">
        <legend class="text-sm">{t("registries.login")}</legend>
        <label class="flex items-center gap-2">
          <input
            type="radio"
            class="radio radio-sm"
            name="registry-auth"
            checked={!form.auth}
            onChange={() => setForm("auth", false)}
          />
          <span class="text-sm">{t("registries.withoutLogin")}</span>
        </label>
        <label class="flex items-center gap-2">
          <input
            type="radio"
            class="radio radio-sm"
            name="registry-auth"
            checked={form.auth}
            onChange={() => setForm("auth", true)}
          />
          <span class="text-sm">{t("registries.withLogin")}</span>
        </label>
      </fieldset>
      <Show when={form.auth}>
        <Field label={t("registries.username")}>
          <Input
            autocomplete="off"
            invalid={invalid("username")}
            value={form.username}
            onInput={(e) => setForm("username", e.currentTarget.value)}
          />
        </Field>
        <Field label={t("registries.password")}>
          <Input
            type="password"
            autocomplete="new-password"
            invalid={invalid("password")}
            placeholder={props.entry?.hasPassword ? t("registries.passwordKeep") : ""}
            value={form.password}
            onInput={(e) => setForm("password", e.currentTarget.value)}
          />
        </Field>
      </Show>
      <Show when={tested()}>
        {(r) => (
          <div role="status" class={r().ok ? "alert alert-success text-sm" : "alert alert-warning text-sm"}>
            {r().ok ? t("registries.testOk") : t("registries.testFailed", { message: r().message })}
          </div>
        )}
      </Show>
    </FormDialog>
  );
}
