import { ApiError, useApi, useI18n, useToast } from "@edelweiss/sdk";
import { createResource, createSignal, For, Show } from "solid-js";
import { DOCKER_HUB, type PullStart, type RegistryList } from "../api";
import { Input, Select } from "../components/Field";
import { Field, FormDialog } from "../components/FormDialog";
import { PullProgress } from "../containers/PullProgress";
import { errorField, errorText } from "../errors";

/** Pulls an image from Docker Hub or a stored registry; while it runs, the dialog shows its progress. */
export function PullDialog(props: { onClose: (pulled: boolean) => void }) {
  const api = useApi();
  const { t } = useI18n();
  const toast = useToast();
  // Without the list Docker Hub is the only choice. A list this user may not read (registries_forbidden) is nothing to
  // report; any other failure (a damaged database, say) is shown as a note in the form.
  const [listError, setListError] = createSignal<unknown>();
  const [list] = createResource(() =>
    api.get<RegistryList>("/registries").catch((err): RegistryList => {
      if (!(err instanceof ApiError && err.code === "registries_forbidden")) setListError(err);
      return { registries: [], writable: false };
    }),
  );
  const [registry, setRegistry] = createSignal("");
  const [image, setImage] = createSignal("");
  const [tag, setTag] = createSignal(""); // empty is latest (the backend's default), shown as the placeholder
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal<unknown>();
  const [job, setJob] = createSignal<PullStart>();
  // a stored Docker Hub login is used for "Docker Hub" anyway (the backend finds it by the host)
  const choices = () => (list()?.registries ?? []).filter((r) => r.address !== DOCKER_HUB);

  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      setJob(
        await api.post<PullStart>("/pulls", {
          registry: registry(),
          image: image().trim(),
          tag: tag().trim(),
        }),
      );
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Show
      when={job()}
      keyed
      fallback={
        <FormDialog
          title={t("pull.dialogTitle")}
          submitLabel={t("pull.submit")}
          busy={busy()}
          error={error() ? errorText(error(), t) : undefined}
          onSubmit={() => void submit()}
          onCancel={() => props.onClose(false)}
        >
          <Field label={t("pull.registry")}>
            {/* selected per option: the list arrives after the select, and select.value set earlier would be lost */}
            <Select onChange={(e) => setRegistry(e.currentTarget.value)}>
              <option value="" selected={registry() === ""}>
                {t("pull.dockerHub")}
              </option>
              <For each={choices()}>
                {(r) => (
                  <option value={r.id} selected={registry() === r.id}>
                    {`${r.name} (${r.address})`}
                  </option>
                )}
              </For>
            </Select>
          </Field>
          <Show when={listError()}>
            {(err) => (
              <p role="status" class="text-xs text-warning">
                {errorText(err(), t)}
              </p>
            )}
          </Show>
          <Field label={t("pull.image")}>
            <Input
              required
              autofocus
              placeholder={t("pull.imageHint")}
              invalid={errorField(error()) === "image"}
              value={image()}
              onInput={(e) => setImage(e.currentTarget.value)}
            />
          </Field>
          <Field label={t("pull.tag")}>
            <Input placeholder="latest" value={tag()} onInput={(e) => setTag(e.currentTarget.value)} />
          </Field>
        </FormDialog>
      }
    >
      {(j) => (
        <PullProgress
          id={j.id}
          image={j.ref}
          onDone={() => {
            toast.success(t("pull.done", { ref: j.ref }));
            props.onClose(true);
          }}
          onError={(message) => {
            setJob(undefined);
            setError(new Error(t("pull.failed", { message })));
          }}
          onClose={() => props.onClose(false)}
        />
      )}
    </Show>
  );
}
