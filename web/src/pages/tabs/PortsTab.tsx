import { useState } from "preact/hooks";
import { api, qs, wtPath } from "../../api/client.ts";
import type { PortMapping, PortPlan, WorktreeDetail } from "../../api/types.ts";
import { Badge, Banner, ErrorBanner, ExternalLink, Loading } from "../../components/ui.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { portStatusView } from "../../lib/status.ts";
import { startOperation } from "../../ops/store.ts";
import { t } from "../../strings.ts";

function PortTable({ rows, caption }: { rows: PortMapping[]; caption: string }) {
  if (rows.length === 0) return <p class="empty">{t.ports.none}</p>;
  return (
    <div class="table-wrap" tabIndex={0} role="region" aria-label={caption}>
      <table class="data">
        <caption class="sr-only">{caption}</caption>
        <thead>
          <tr>
            <th scope="col">{t.ports.colService}</th>
            <th scope="col" class="num">{t.ports.colContainerPort}</th>
            <th scope="col">{t.ports.colConfigured}</th>
            <th scope="col">{t.ports.colEffective}</th>
            <th scope="col">{t.ports.colStatus}</th>
            <th scope="col">{t.ports.colUrl}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((p, i) => (
            <tr key={i}>
              <th scope="row">{p.service}</th>
              <td class="num mono">{p.target}{p.protocol && p.protocol !== "tcp" ? `/${p.protocol}` : ""}</td>
              <td class="mono">{p.configured || t.ports.random}</td>
              <td class="mono">{p.running || p.effective || "—"}{p.host_ip === "0.0.0.0" ? <span class="small muted"> ({t.ports.hostIpAll})</span> : null}</td>
              <td><Badge view={portStatusView(p.status)} /></td>
              <td>{p.url ? <ExternalLink href={p.url} label={p.service}>{p.url.replace(/^https?:\/\//, "")}</ExternalLink> : t.ports.noUrl}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function PortsTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const res = useResource<PortPlan>(() => api.get<PortPlan>(`${wtPath(project, w.id)}/ports`), [project, w.id], { project, worktree: w.id });
  const [preview, setPreview] = useState<PortPlan | null>(null);
  const load = useAction(async () => { setPreview(await api.get<PortPlan>(`${wtPath(project, w.id)}/ports${qs({ auto_ports: true })}`)); });
  const start = useAction(async () => { await startOperation(project, w.id, "up", { auto_ports: true }); setPreview(null); });
  if (res.loading && !res.data) return <Loading />;
  if (res.error) return <ErrorBanner error={res.error} />;
  return (
    <div class="stack">
      <PortTable rows={res.data?.ports ?? []} caption={t.ports.title} />
      <div>
        <button type="button" class="btn" onClick={() => void load.run()} disabled={load.busy}>{t.ports.preview}</button>
      </div>
      <ErrorBanner error={load.error ?? start.error} />
      {preview ? (
        <section aria-labelledby="ports-prev">
          <h2 id="ports-prev" tabIndex={-1}>{t.ports.previewTitle}</h2>
          <Banner tone="info"><p>{t.ports.previewNote}</p></Banner>
          <PortTable rows={preview.ports} caption={t.ports.previewTitle} />
          <p><button type="button" class="btn primary" disabled={start.busy || w.state === "running"} onClick={() => void start.run()}>{t.ports.startWith}</button></p>
        </section>
      ) : null}
    </div>
  );
}
