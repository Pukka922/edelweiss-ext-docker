import { useI18n } from "@edelweiss/sdk";
import {
  IconChevronLeft,
  IconChevronRight,
  IconChevronsLeft,
  IconChevronsRight,
} from "@tabler/icons-solidjs";
import { Show } from "solid-js";

/** First, previous, next and last page of a list, with the page it shows. Not shown for a single page. */
export function Pager(props: { page: number; pages: number; onPage: (page: number) => void }) {
  const { t } = useI18n();
  return (
    <Show when={props.pages > 1}>
      <div class="flex flex-wrap items-center justify-end">
        <div class="join">
          <button
            type="button"
            class="btn btn-sm join-item"
            aria-label={t("list.first")}
            disabled={props.page <= 1}
            onClick={() => props.onPage(1)}
          >
            <IconChevronsLeft size={16} aria-hidden="true" />
          </button>
          <button
            type="button"
            class="btn btn-sm join-item"
            aria-label={t("list.prev")}
            disabled={props.page <= 1}
            onClick={() => props.onPage(props.page - 1)}
          >
            <IconChevronLeft size={16} aria-hidden="true" />
          </button>
          <span class="btn btn-sm join-item pointer-events-none">
            {t("list.page", { page: props.page, pages: props.pages })}
          </span>
          <button
            type="button"
            class="btn btn-sm join-item"
            aria-label={t("list.next")}
            disabled={props.page >= props.pages}
            onClick={() => props.onPage(props.page + 1)}
          >
            <IconChevronRight size={16} aria-hidden="true" />
          </button>
          <button
            type="button"
            class="btn btn-sm join-item"
            aria-label={t("list.last")}
            disabled={props.page >= props.pages}
            onClick={() => props.onPage(props.pages)}
          >
            <IconChevronsRight size={16} aria-hidden="true" />
          </button>
        </div>
      </div>
    </Show>
  );
}
