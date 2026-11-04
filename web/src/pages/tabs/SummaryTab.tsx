import { api, projectPath, qs, wtPath } from "../../api/client.ts";
import type { LifeReport, LifeWorktree, Services, WorktreeDetail, WtWarning } from "../../api/types.ts";
import { KeepDialog, ParkDialog, unkeep } from "../../components/LifecycleDialogs.tsx";
import { activityText, shortDate, sizeText } from "../../lib/lifecycle.ts";
import { useState } from "preact/hooks";
import { Banner, ErrorBanner, ExternalLink } from "../../components/ui.tsx";
import { CopyCommand } from "../../components/CopyCommand.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { announce } from "../../lib/live.ts";
import { href } from "../../lib/router.ts";
import { ServicesTab } from "./ServicesTab.tsx";
import { PortsTab } from "./PortsTab.tsx";
import { containerCounts } from "../../lib/status.ts";
import { shortSha } from "../../lib/overview.ts";
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

export function SummaryTab({ project, w, lock }: { project: string; w: WorktreeDetail; lock: boolean }) {
  const svc = useResource<Services>(() => api.get<Services>(`${wtPath(project, w.id)}/services`), [project, w.id]);
  const counts = containerCounts(w);
  const warnings = serviceWarnings(svc.data);
  const src = w.env_file?.source ?? "none";
  return (
    <div class="stack">
      {svc.data && (warnings.length > 0 || (w.warnings ?? []).length > 0) ? (
        <section aria-labelledby="sum-warn">
          <h2 id="sum-warn">{t.summary.warnings}</h2>
          {warnings.map((x, i) => <Banner key={i} tone="warn"><p>{x}</p></Banner>)}
          {(w.warnings ?? []).map((x, i) => x.kind === "url-in-data" && x.previous && x.current
            ? <UrlInDataCard key={"w" + i} project={project} w={w} warning={x} />
            : <Banner key={"w" + i} tone="warn"><p>{x.message}</p>{x.fix ? <p>{x.fix}</p> : null}</Banner>)}
        </section>
      ) : null}

      {!w.main ? <LifecycleCard project={project} w={w} /> : null}

      <section aria-labelledby="sum-svc">
        <h2 id="sum-svc">{t.services.title}</h2>
        <ServicesTab project={project} w={w} lock={lock} />
      </section>

      <section aria-labelledby="sum-ports">
        <h2 id="sum-ports">{t.ports.title}</h2>
        <PortsTab project={project} w={w} />
      </section>

      <section aria-labelledby="sum-facts">
        <h2 id="sum-facts">{t.summary.facts}</h2>
        <dl class="facts">
          <dt>{t.summary.branch}</dt><dd>{w.branch ?? "—"}{w.detached ? " (detached)" : ""}</dd>
          <dt>{t.summary.commit}</dt><dd class="mono">{shortSha(w.head) || "—"}</dd>
          <dt>{t.summary.path}</dt><dd class="mono">{w.path}</dd>
          <dt>{t.summary.compose}</dt><dd class="mono">{w.compose_project ?? "—"}</dd>
          <dt>{t.summary.envFile}</dt><dd>{t.summary.envSource[src] ?? src}{w.env_file?.path ? <> — <span class="mono">{w.env_file.path}</span></> : null}</dd>
          <dt>{t.summary.containers}</dt><dd>{counts ? `${counts.running}/${counts.total}` : "—"}</dd>
          <dt>{t.summary.owner}</dt><dd>{w.owner ?? <span class="muted">{t.summary.noOwner}</span>}</dd>
          <dt>{t.summary.domains}</dt>
          <dd>
            {(w.proxy_urls ?? []).length > 0
              ? <ul class="plain">{(w.proxy_urls ?? []).map((l, i) => <li key={i}><ExternalLink href={l.url} label={l.service}>{l.url.replace(/^https?:\/\//, "")}</ExternalLink>{l.https_url ? <> · <ExternalLink href={l.https_url} label={`${l.service} HTTPS`}>HTTPS</ExternalLink></> : null}{l.default ? null : <span class="small muted"> {l.service}</span>}</li>)}</ul>
              : <span class="muted">{t.summary.noDomains}</span>}
          </dd>
        </dl>
      </section>
    </div>
  );
}

// Q-020: the application keeps its own address in its data and the instance now answers elsewhere (board
// "V-05 Instancia › Resumen con aviso Q-020"). Theo only warns; "It is fixed" records the current address.
function UrlInDataCard({ project, w, warning }: { project: string; w: WorktreeDetail; warning: WtWarning }) {
  const u = t.urlInData;
  const app = /^(\S+) stores its own address/.exec(warning.message)?.[1] ?? warning.service ?? "";
  const done = useAction(async () => {
    await api.patch(`${wtPath(project, w.id)}/settings`, { data_address_updated: true });
    announce(u.doneMsg);
  });
  return (
    <section class="card stack url-in-data" aria-labelledby={`uid-${w.id}`}>
      <div class="row">
        <span class="badge warn">{u.chip}</span>
        <h3 id={`uid-${w.id}`}>{u.title(app)}</h3>
      </div>
      <p class="small">{u.body(app)}</p>
      <dl class="facts small">
        <dt>{u.before}</dt><dd class="mono">{warning.previous}</dd>
        <dt>{u.now}</dt><dd class="mono">{warning.current}</dd>
      </dl>
      <p class="small"><strong>{u.how}</strong></p>
      {warning.command ? <><p class="small">{u.command}</p><pre class="mono cmd">{warning.command}</pre></> : null}
      <p class="small">{warning.command ? u.envHint : u.envOnly}</p>
      <ErrorBanner error={done.error} />
      <div class="row">
        {warning.command ? <CopyCommand command={warning.command} label={u.copy} /> : null}
        <a class="btn sm" href={href.detail(project, w.id, "config")}>{u.variables}</a>
        <button type="button" class="btn sm" disabled={done.busy} onClick={() => void done.run()}>{u.done}</button>
        <a class="btn sm quiet" href={href.projectSettings(project)}>{u.notMine}</a>
      </div>
    </section>
  );
}

// Board "V-05 Instancia › Ciclo de vida": state and why, last activity, base, upstream, origin and size, with Park and
// Keep; a banner when the instance runs in an inactive worktree.
function LifecycleCard({ project, w }: { project: string; w: WorktreeDetail }) {
  const res = useResource<LifeReport>(() => api.get<LifeReport>(`${projectPath(project)}/lifecycle${qs({ size: true })}`), [project, w.id]);
  const [parking, setParking] = useState(false);
  const [keeping, setKeeping] = useState(false);
  const l: LifeWorktree | undefined = res.data?.worktrees.find((x) => x.id === w.id);
  const unkeepAction = useAction(async () => { if (l) { await unkeep(project, l); res.reload(); } });
  if (!l || !l.state) return null;
  const L = t.life;
  const base = l.base ? `${l.base}${l.ahead >= 0 ? ` · ${l.ahead} ↑ · ${l.behind} ↓` : ""}` : "—";
  const upstream = l.upstream ? `${l.upstream}${l.upstream_gone ? ` · ${t.cleanup.upstreamGone}` : l.unpushed > 0 ? ` · ${L.notPushed(l.unpushed)}` : ` · ${L.pushed}`}` : L.noUpstream;
  const idle = l.running && (w.warnings ?? []).some((x) => x.kind === "instance-idle");
  return (
    <section aria-labelledby="sum-life" class="stack-sm">
      <h2 id="sum-life">{L.cardTitle}</h2>
      {idle ? <Banner tone="warn"><p>{L.idleBanner(l.idle_days)}</p></Banner> : null}
      <dl class="facts">
        <dt>{L.cardState}</dt><dd><strong>{L.state[l.state]}</strong>{l.reason ? <span class="muted">: {l.reason}</span> : null}</dd>
        <dt>{L.cardActivity}</dt><dd>{activityText(l)}{l.activity_at && l.state !== "kept" ? <span class="muted"> ({shortDate(l.activity_at)})</span> : null}</dd>
        <dt>{L.cardBase}</dt><dd class="mono">{base}</dd>
        <dt>{L.cardUpstream}</dt><dd class="mono">{upstream}</dd>
        <dt>{L.cardOrigin}</dt><dd>{l.origin === "theo" ? L.createdByTheo(shortDate(l.created_at), w.owner ?? "") : L.foundByTheo}</dd>
        <dt>{L.cardSize}</dt><dd>{sizeText(l.size_bytes)}</dd>
      </dl>
      <ErrorBanner error={unkeepAction.error} />
      <div class="row">
        {w.kind === "worktree" && l.state !== "missing" ? <button type="button" class="btn sm" disabled={l.changes > 0} onClick={() => setParking(true)}>{L.park}</button> : null}
        {l.state === "kept" ? <button type="button" class="btn sm" disabled={unkeepAction.busy} onClick={() => void unkeepAction.run()}>{L.unkeep}</button>
          : <button type="button" class="btn sm" onClick={() => setKeeping(true)}>{L.keep}</button>}
      </div>
      {parking ? <ParkDialog project={project} w={l} onClose={() => setParking(false)} onDone={() => { location.hash = href.project(project); }} /> : null}
      {keeping ? <KeepDialog project={project} w={l} onClose={() => setKeeping(false)} onDone={res.reload} /> : null}
    </section>
  );
}
