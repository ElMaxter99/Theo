import { useState } from "preact/hooks";
import { api, qs, wtPath } from "../../api/client.ts";
import type { HistoryEvent, WorktreeDetail } from "../../api/types.ts";
import { Badge, ErrorBanner, Loading } from "../../components/ui.tsx";
import { useResource } from "../../lib/hooks.ts";
import { formatTime } from "../../lib/format.ts";
import { t } from "../../strings.ts";

const PAGE = 20;

export function HistoryTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const res = useResource<HistoryEvent[]>(() => api.get<HistoryEvent[]>(`${wtPath(project, w.id)}/history${qs({ limit: 200 })}`), [project, w.id], { project, worktree: w.id });
  const [filter, setFilter] = useState("");
  const [count, setCount] = useState(PAGE);
  if (res.loading && !res.data) return <Loading />;
  if (res.error) return <ErrorBanner error={res.error} />;
  const all = (res.data ?? []).filter((e) => !filter || e.result === filter);
  const rows = all.slice(0, count);
  return (
    <div class="stack">
      <div class="field">
        <label for="hist-filter">{t.history.filter}</label>
        <select id="hist-filter" class="input auto" value={filter} onChange={(e) => { setFilter((e.currentTarget as HTMLSelectElement).value); setCount(PAGE); }}>
          <option value="">{t.history.all}</option>
          <option value="ok">{t.history.ok}</option>
          <option value="error">{t.history.error}</option>
        </select>
      </div>
      {all.length === 0 ? <p class="empty">{t.history.none}</p> : (
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
              {rows.map((e, i) => (
                <tr key={i}>
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
      {all.length > count ? (
        <p class="row">
          <button type="button" class="btn" onClick={() => setCount(count + PAGE)}>{t.history.more}</button>
          <span class="muted small">{t.history.shown(rows.length, all.length)}</span>
        </p>
      ) : null}
    </div>
  );
}
