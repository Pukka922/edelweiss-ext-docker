import { useI18n } from "@edelweiss/sdk";
import { createSignal, type JSX, Show } from "solid-js";
import type { StatsSample, StreamError } from "../api";
import { formatBytes, formatRate } from "../format";
import { pushSample } from "../history";
import { listen } from "../live";
import { Sparkline } from "../Sparkline";

function Tile(props: {
  title: string;
  value: string;
  values: number[];
  max?: number;
  children?: JSX.Element;
}) {
  return (
    <div class="card bg-base-200">
      <div class="card-body gap-1 p-4">
        <h3 class="text-sm text-base-content/70">{props.title}</h3>
        <p class="text-lg font-semibold">{props.value}</p>
        <Sparkline values={props.values} max={props.max ?? Math.max(1, ...props.values)} />
        {props.children}
      </div>
    </div>
  );
}

function Stream(props: { id: string }) {
  const { t, locale } = useI18n();
  const [latest, setLatest] = createSignal<StatsSample>();
  const [cpu, setCpu] = createSignal<number[]>([]);
  const [mem, setMem] = createSignal<number[]>([]);
  const [net, setNet] = createSignal<number[]>([]);
  const [disk, setDisk] = createSignal<number[]>([]);
  const [stopped, setStopped] = createSignal(false);
  const [failed, setFailed] = createSignal<string>();
  const close = listen(`/containers/${encodeURIComponent(props.id)}/stats`, {
    error: (data) => {
      setFailed((data as Partial<StreamError>).message ?? "");
      close();
    },
    stats: (data) => {
      const s = data as StatsSample;
      setLatest(s);
      setCpu((v) => pushSample(v, s.cpuPercent));
      setMem((v) => pushSample(v, s.memUsage ?? 0));
      setNet((v) => pushSample(v, s.netRx + s.netTx));
      setDisk((v) => pushSample(v, s.blkRead + s.blkWrite));
    },
    stopped: () => {
      setStopped(true);
      close();
    },
  });
  const memText = (s: StatsSample) =>
    s.memUsage === null || s.memLimit === null
      ? "–"
      : `${formatBytes(s.memUsage, locale())} / ${formatBytes(s.memLimit, locale())}`;
  return (
    <Show when={!stopped()} fallback={<p class="text-base-content/70">{t("stats.stopped")}</p>}>
      <Show when={failed() !== undefined}>
        <p role="alert" class="text-sm text-error">
          {t("stats.error", { message: failed() ?? "" })}
        </p>
      </Show>
      <Show
        when={latest()}
        fallback={
          <Show when={failed() === undefined}>
            <p class="text-base-content/70">{t("stats.waiting")}</p>
          </Show>
        }
      >
        {(s) => (
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            <Tile
              title={t("stats.cpu")}
              value={`${s().cpuPercent} %`}
              values={cpu()}
              max={Math.max(100, ...cpu())}
            />
            <Tile
              title={t("stats.memory")}
              value={memText(s())}
              values={mem()}
              max={s().memLimit ?? undefined}
            >
              <Show when={s().memUsage === null}>
                <p class="text-xs text-base-content/70">{t("stats.noMemory")}</p>
              </Show>
            </Tile>
            <Tile
              title={t("stats.network")}
              value={`${t("stats.rx")} ${formatRate(s().netRx, locale())} · ${t("stats.tx")} ${formatRate(s().netTx, locale())}`}
              values={net()}
            />
            <Tile
              title={t("stats.disk")}
              value={`${t("stats.read")} ${formatRate(s().blkRead, locale())} · ${t("stats.write")} ${formatRate(s().blkWrite, locale())}`}
              values={disk()}
            />
            <div class="card bg-base-200">
              <div class="card-body gap-1 p-4">
                <h3 class="text-sm text-base-content/70">{t("stats.pids")}</h3>
                <p class="text-lg font-semibold">{s().pids}</p>
              </div>
            </div>
          </div>
        )}
      </Show>
    </Show>
  );
}

/**
 * The live statistics of a running container. Every run of the container gets its own stream: it ends (stopped, or an
 * error) and stays over, and when the container runs again (running goes from false to true) the tab builds it anew.
 */
export function StatsTab(props: { id: string; running: boolean }) {
  const { t } = useI18n();
  return (
    <Show when={props.running} fallback={<p class="text-base-content/70">{t("stats.stopped")}</p>}>
      <Stream id={props.id} />
    </Show>
  );
}
