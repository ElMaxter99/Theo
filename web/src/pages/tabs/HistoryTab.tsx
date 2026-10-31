import { useEffect, useMemo, useState } from "preact/hooks";
import { ApiError, getWithHeaders, qs, wtPath } from "../../api/client.ts";
import type { HistoryEvent, WorktreeDetail } from "../../api/types.ts";
import { Badge, ErrorBanner, Loading } from "../../components/ui.tsx";
import { useResource } from "../../lib/hooks.ts";
import { formatTime } from "../../lib/format.ts";
import { announce } from "../../lib/live.ts";
import { t } from "../../strings.ts";

const PAGE = 20;

interface Page { events: HistoryEvent[]; next: string | null }

/** The body is the plain array (oldest first); the paging travels in the headers Theo-History-Next (cursor of the older page, absent on the oldest). */
async function fetchPage(url: string): Promise<Page> {
  const { data, headers } = await getWithHeaders<HistoryEvent[]>(url);
  return { events: Array.isArray(data) ? data : [], next: headers.get("Theo-History-Next") };
}

const keyOf = (e: HistoryEvent) => `${e.time}|${e.operation}|${e.result}|${e.workspace ?? ""}`;

export function HistoryTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const base = `${wtPath(project, w.id)}/history`;
  // First page: the newest events. Older pages are requested with `before` = the time of the oldest event we have.
  const first = useResource<Page>(() => fetchPage(`${base}${qs({ limit: PAGE })}`), [project, w.id], { project, worktree: w.id });
  const res = { ...first, data: first.data?.events };
  const [older, setOlder] = useState<HistoryEvent[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [moreBusy, setMoreBusy] = useState(false);
  const [moreError, setMoreError] = useState<ApiError | undefined>();
  const [filter, setFilter] = useState("");

  // A new first page (the instance changed) starts the list again.
  useEffect(() => { setOlder([]); setCursor(first.data?.next ?? null); }, [first.data]);

  const newestFirst = useMemo(() => {
    const seen = new Set<string>();
    const out: HistoryEvent[] = [];
    for (const e of [...(res.data ?? [])].reverse().concat(older)) {
      const k = keyOf(e);
      if (!seen.has(k)) { seen.add(k); out.push(e); }
    }
    return out;
  }, [res.data, older]);

  if (res.loading && !res.data) return <Loading />;
  if (res.error && !res.data) return <ErrorBanner error={res.error} />;

  const canLoadMore = cursor !== null;
  const rows = newestFirst.filter((e) => !filter || e.result === filter);

  const loadMore = async () => {
    if (cursor === null) return;
    setMoreBusy(true);
    setMoreError(undefined);
    try {
      const page = await fetchPage(`${base}${qs({ limit: PAGE, before: cursor })}`);
      const have = new Set(newestFirst.map(keyOf));
      const fresh = [...page.events].reverse().filter((e) => !have.has(keyOf(e)));
      setOlder([...older, ...fresh]);
      setCursor(page.next);
      announce(fresh.length > 0 ? t.history.loaded(newestFirst.length + fresh.length) : t.history.noMore);
    } catch (e) {
      setMoreError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
    } finally {
      setMoreBusy(false);
    }
  };

  return (
    <div class="stack">
      <div class="field">
        <label for="hist-filter">{t.history.filter}</label>
        <select id="hist-filter" class="input auto" value={filter} onChange={(e) => setFilter((e.currentTarget as HTMLSelectElement).value)}>
          <option value="">{t.history.all}</option>
          <option value="ok">{t.history.ok}</option>
          <option value="error">{t.history.error}</option>
        </select>
      </div>
      {rows.length === 0 ? <p class="empty">{t.history.none}</p> : (
        <div class="table-wrap" tabIndex={0} role="region" aria-label={t.history.title}>
          <table class="data">
            <caption class="sr-only">{t.history.title}</caption>
            <thead>
              <tr>
                <th scope="col">{t.history.colOperation}</th>
                <th scope="col">{t.history.colResult}</th>
                <th scope="col">{t.history.colTime}</th>
                <th scope="col">{t.history.colUser}</th>
                <th scope="col">{t.history.colError}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((e) => (
                <tr key={keyOf(e)}>
                  <th scope="row">{e.operation}</th>
                  <td><Badge view={e.result === "ok" ? { label: t.history.ok, icon: "ok", tone: "ok" } : { label: t.history.error, icon: "error", tone: "error" }} /></td>
                  <td class="nowrap">{formatTime(e.time)}</td>
                  <td>{e.user ?? "—"}</td>
                  <td class="small">{e.error ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <ErrorBanner error={moreError} />
      <p class="row">
        {canLoadMore ? <button type="button" class="btn" disabled={moreBusy} onClick={() => void loadMore()}>{moreBusy ? t.history.loadingMore : t.history.loadMore}</button> : null}
        <span class="muted small">{canLoadMore ? t.history.loaded(newestFirst.length) : newestFirst.length > 0 ? t.history.noMore : ""}</span>
      </p>
    </div>
  );
}
