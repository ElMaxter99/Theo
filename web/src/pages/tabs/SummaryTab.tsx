import { api, wtPath } from "../../api/client.ts";
import type { InstanceSettings, Services, WorktreeDetail } from "../../api/types.ts";
import { Banner, ErrorBanner, Loading } from "../../components/ui.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { announce } from "../../lib/live.ts";
import { containerCounts } from "../../lib/status.ts";
import { t } from "../../strings.ts";

export interface Warning { text: string }

export function serviceWarnings(s: Services | undefined): string[] {
  const out: string[] = [];
  for (const svc of s?.services ?? []) {
    for (const m of svc.mounts ?? []) {
      if (m.source && /docker\.sock$/.test(m.source)) out.push(t.summary.warnSocket(svc.name));
      else if (m.outside_workspace) out.push(t.summary.warnOutside(svc.name, m.source ?? m.target ?? ""));
    }
    for (const p of svc.ports ?? []) {
      if (p.published && (p.host_ip === "0.0.0.0" || p.host_ip === "" || p.host_ip === undefined) && p.published !== "") out.push(t.summary.warnPublic(svc.name, p.published));
    }
  }
  return out;
}

export function SummaryTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const svc = useResource<Services>(() => api.get<Services>(`${wtPath(project, w.id)}/services`), [project, w.id]);
  const settings = useResource<InstanceSettings>(() => api.get<InstanceSettings>(`${wtPath(project, w.id)}/settings`), [project, w.id]);
  const counts = containerCounts(w);
  const toggle = useAction(async (v: boolean) => {
    await api.patch(`${wtPath(project, w.id)}/settings`, { neutralize_container_names: v });
    announce(t.summary.settingSaved);
    settings.reload();
  });
  const warnings = serviceWarnings(svc.data);
  const src = w.env_file?.source ?? "none";
  return (
    <div class="stack">
      <dl class="facts">
        <dt>{t.summary.branch}</dt><dd>{w.branch ?? "—"}{w.detached ? " (detached)" : ""}</dd>
        <dt>{t.summary.commit}</dt><dd class="mono">{w.head ?? "—"}</dd>
        <dt>{t.summary.path}</dt><dd class="mono">{w.path}</dd>
        <dt>{t.summary.compose}</dt><dd class="mono">{w.compose_project ?? "—"}</dd>
        <dt>{t.summary.envFile}</dt><dd>{t.summary.envSource[src] ?? src}{w.env_file?.path ? <> — <span class="mono">{w.env_file.path}</span></> : null}</dd>
        <dt>{t.summary.containers}</dt><dd>{counts ? `${counts.running}/${counts.total}` : "—"}</dd>
      </dl>

      <section aria-labelledby="sum-warn">
        <h2 id="sum-warn">{t.summary.warnings}</h2>
        {svc.loading && !svc.data ? <Loading /> : null}
        {svc.data && warnings.length === 0 ? <p class="muted">{t.summary.noWarnings}</p> : null}
        {warnings.map((x, i) => <Banner key={i} tone="warn">{<p>{x}</p>}</Banner>)}
      </section>

      <section aria-labelledby="sum-set">
        <h2 id="sum-set">{t.summary.settings}</h2>
        <div class="check">
          <input id="neutralize" type="checkbox" checked={!!settings.data?.neutralize_container_names} disabled={!settings.data || toggle.busy}
            aria-describedby="neutralize-hint" onChange={(e) => void toggle.run((e.currentTarget as HTMLInputElement).checked)} />
          <div>
            <label for="neutralize">{t.summary.neutralize}</label>
            <p class="hint" id="neutralize-hint">{t.summary.neutralizeHint}</p>
          </div>
        </div>
        <ErrorBanner error={toggle.error} />
      </section>
    </div>
  );
}
