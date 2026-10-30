import { useState } from "preact/hooks";
import { api, wtPath } from "../../api/client.ts";
import type { VolumeInfo, WorktreeDetail } from "../../api/types.ts";
import { Badge, ErrorBanner, Loading } from "../../components/ui.tsx";
import { TypedConfirmDialog } from "../../components/Dialog.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { announce } from "../../lib/live.ts";
import { containerCounts, volumeStatusView } from "../../lib/status.ts";
import { ApiError } from "../../api/client.ts";
import type { ApiErrorBody } from "../../api/types.ts";
import { t } from "../../strings.ts";

export function VolumesTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const base = `${wtPath(project, w.id)}/volumes`;
  const res = useResource<VolumeInfo[]>(() => api.get<VolumeInfo[]>(base), [project, w.id], { project, worktree: w.id });
  const [typed, setTyped] = useState<ApiErrorBody | null>(null);
  const counts = containerCounts(w);
  const hasContainers = w.state !== "absent" && (counts ? counts.total > 0 : w.state !== "unknown");
  const vols = res.data ?? [];
  const deletable = vols.some((v) => v.status !== "external");

  // First call WITHOUT confirm: the server refuses and describes exactly what would be deleted (contract section 6).
  const ask = useAction(async () => {
    try {
      await api.post(`${base}/purge`, {});
      announce(t.volumes.purged);
      res.reload();
    } catch (e) {
      if (e instanceof ApiError && e.code === "confirmation_required") { setTyped(e.body); return; }
      throw e;
    }
  });

  return (
    <div class="stack">
      {res.loading && !res.data ? <Loading /> : null}
      <ErrorBanner error={res.error} />
      {res.data && vols.length === 0 ? <p class="empty">{t.volumes.none}</p> : null}
      {vols.length > 0 ? (
        <div class="table-wrap" tabIndex={0} role="region" aria-label={t.volumes.title}>
          <table class="data">
            <caption class="sr-only">{t.volumes.title}</caption>
            <thead>
              <tr>
                <th scope="col">{t.volumes.colName}</th>
                <th scope="col">{t.volumes.colStatus}</th>
                <th scope="col">{t.volumes.colContainers}</th>
              </tr>
            </thead>
            <tbody>
              {vols.map((v) => (
                <tr key={v.name}>
                  <th scope="row" class="mono">{v.name}{v.volume && v.volume !== v.name ? <div class="small muted">{v.volume}</div> : null}</th>
                  <td>
                    <Badge view={volumeStatusView(v.status)} />
                    {v.status === "external" ? <div class="small muted">{t.volumes.external}</div> : null}
                    {v.created === false ? <div class="small muted">{t.volumes.notCreated}</div> : null}
                  </td>
                  <td class="small mono">{(v.containers ?? []).join(", ") || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      <ErrorBanner error={ask.error} />
      {deletable ? (
        <div>
          <button type="button" class="btn danger" disabled={hasContainers || ask.busy} aria-describedby="purge-hint" onClick={() => void ask.run()}>{t.volumes.purge}</button>
          {hasContainers ? <p class="hint" id="purge-hint">{t.volumes.purgeDisabled}</p> : <p class="hint" id="purge-hint">{t.volumes.historyNote}</p>}
        </div>
      ) : null}
      {typed ? (
        <TypedConfirmDialog
          title={t.volumes.purgeTitle(w.compose_project ?? w.branch ?? w.id)}
          lead={typed.message}
          expected={typed.confirm?.expected ?? (w.compose_project ?? "")}
          prompt={typed.confirm?.prompt}
          affected={typed.affected}
          preserved={typed.preserved}
          confirmLabel={t.volumes.purge.replace("…", "")}
          onClose={() => setTyped(null)}
          onConfirm={async (text) => { await api.post(`${base}/purge`, { confirm: text }); announce(t.volumes.purged); res.reload(); }}
        />
      ) : null}
    </div>
  );
}
