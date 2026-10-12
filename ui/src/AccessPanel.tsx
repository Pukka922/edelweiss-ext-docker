import { useI18n, useSession } from "@edelweiss/sdk";
import { Show } from "solid-js";
import type { Status } from "./api";

const code = "rounded bg-base-200 px-2 py-1 font-mono text-sm text-base-content";

/** What to do when Docker is not running or refuses this user. */
export function AccessPanel(props: { status: Status; onRetry: () => void }) {
  const { t } = useI18n();
  const session = useSession();
  const forbidden = () => props.status.docker === "forbidden";
  return (
    <div role="alert" class="alert alert-warning flex flex-col items-start gap-2">
      <h2 class="font-semibold">{forbidden() ? t("access.forbiddenTitle") : t("access.unavailableTitle")}</h2>
      <Show
        when={forbidden()}
        fallback={
          <>
            <p>{t("access.unavailable")}</p>
            <code class={code}>rc-service docker start</code>
            <p class="text-sm">{t("access.unavailableInstall")}</p>
            <code class={code}>apk add docker && rc-update add docker default</code>
          </>
        }
      >
        <p>{t("access.forbidden")}</p>
        <code class={code}>{`addgroup ${session.user()?.name ?? "<user>"} docker`}</code>
        <p class="text-sm">{t("access.forbiddenRelogin")}</p>
      </Show>
      <Show when={props.status.message}>
        <p class="text-xs opacity-70">{props.status.message}</p>
      </Show>
      <div class="flex gap-2">
        <Show when={forbidden() && session.canAdmin() && !session.admin()}>
          <button type="button" class="btn btn-sm btn-warning" onClick={() => void session.requestAdmin()}>
            {t("access.adminMode")}
          </button>
        </Show>
        <button type="button" class="btn btn-sm" onClick={() => props.onRetry()}>
          {t("access.retry")}
        </button>
      </div>
    </div>
  );
}
