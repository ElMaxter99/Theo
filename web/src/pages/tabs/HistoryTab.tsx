import { useEffect, useMemo, useState } from "preact/hooks";
import { api, ApiError, qs, wtPath } from "../../api/client.ts";
import type { HistoryEvent, WorktreeDetail } from "../../api/types.ts";
import { Badge, ErrorBanner, Loading } from "../../components/ui.tsx";
import { useResource } from "../../lib/hooks.ts";
import { formatTime } from "../../lib/format.ts";
import { announce } from "../../lib/live.ts";
import { t } from "../../strings.ts";

const PAGE = 20;

/** The server answers with the events, oldest first (same as `theo history --json`); an object with `events` is accepted too. */
function eventsOf(body: HistoryEvent[] | { events?: HistoryEvent[]; items?: HistoryEvent[] }): HistoryEvent[] {
  return Array.isArray(body) ? body : body.events ?? body.items ?? [];
}

const keyOf = (e: HistoryEvent) => `${e.time}|${e.operation}|${e.result}|${e.workspace ?? ""}`;

export function HistoryTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const base = `${wtPath(project, w.id)}/history`;
  // First page: the newest events. Older pages are requested with `before` = the time of the oldest event we have.
  const res = useResource<HistoryEvent[]>(async () => eventsOf(await api.get(`${base}${qs({ limit: PAGE })}`)), [project, w.id], { project, worktree: w.id });
  const [older, setOlder] = useState<HistoryEvent[]>([]);
  const [exhausted, setExhausted] = useState(false);
  const [moreBusy, setMoreBusy] = useState(false);
  const [moreError, setMoreError] = useState<ApiError | undefined>();
  const [filter, setFilter] = useState("");

  // A new first page (the instance changed) starts the list again.
  useEffect(() => { setOlder([]); setExhausted(false); }, [res.data]);

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

  const firstFull = (res.data?.length ?? 0) >= PAGE;
  const canLoadMore = !exhausted && firstFull;
  const rows = newestFirst.filter((e) => !filter || e.result === filter);

  const loadMore = async () => {
    const oldest = newestFirst[newestFirst.length - 1];
    if (!oldest) return;
    setMoreBusy(true);
    setMoreError(undefined);
    try {
      const page = eventsOf(await api.get(`${base}${qs({ limit: PAGE, before: oldest.time })}`));
      const have = new Set(newestFirst.map(keyOf));
      const fresh = [...page].reverse().filter((e) => !have.has(keyOf(e)));
      setOlder([...older, ...fresh]);
      if (fresh.length === 0 || page.length < PAGE) setExhausted(true);
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
