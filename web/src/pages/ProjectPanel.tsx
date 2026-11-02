import { useEffect, useMemo, useState } from "preact/hooks";
import { api, ApiError, projectPath } from "../api/client.ts";
import type { Overview, ProxyStatus, Worktree } from "../api/types.ts";
import { Badge, Banner, Crumbs, ErrorBanner, ExternalLink, Loading, PageHead } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { RemoveFlow } from "../components/RemoveFlow.tsx";
import { useResource } from "../lib/hooks.ts";
import { href } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { conflictsOf, urlsOf } from "../lib/overview.ts";
import { containerCounts, isActiveOp, worktreeStatus } from "../lib/status.ts";
import { rememberWorktrees, startOperation, useOps } from "../ops/store.ts";
import type { LifecycleKind } from "../ops/store.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function wtName(w: Worktree): string {
  return w.branch || (w.detached ? t.panel.detached : "") || w.path.split(/[\\/]/).filter(Boolean).pop() || w.id;
}

export function ProjectPanel({ project, onRefreshMeta }: { project: string; onRefreshMeta: () => void }) {
  const res = useResource<Overview>(() => api.get<Overview>(`${projectPath(project)}/overview`), [project], { project });
  const ops = useOps();
  const [filter, setFilter] = useState("");
  const [owner, setOwner] = useState("");
  const proxy = useResource<ProxyStatus>(() => api.get<ProxyStatus>("/proxy"), [], { project });
  const [removing, setRemoving] = useState<Worktree | null>(null);
  const [actionError, setActionError] = useState<ApiError | undefined>();
  useEffect(() => setTitle([project]), [project]);
  useEffect(() => { if (res.data) rememberWorktrees(res.data.worktrees); }, [res.data]);

  // When an operation of this project finishes, the list is refreshed (the server also emits state.changed).
  const activeKey = ops.filter(isActiveOp).map((o) => o.id).join(",");
  useEffect(() => { if (res.data) res.reload(); /* eslint-disable-next-line */ }, [activeKey]);

  const wts = res.data?.worktrees ?? [];
  const owners = useMemo(() => [...new Set(wts.map((w) => w.owner).filter((o): o is string => !!o))].sort(), [wts]);
  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return wts.filter((w) => {
      if (owner === "-" ? !!w.owner : owner && w.owner !== owner) return false;
      if (!q) return true;
      return (w.branch ?? "").toLowerCase().includes(q) || w.path.toLowerCase().includes(q) || (w.owner ?? "").toLowerCase().includes(q);
    });
  }, [wts, filter, owner]);
  const pausable = wts.filter((w) => (w.state === "running" || w.state === "partial" || w.state === "unhealthy") && !w.busy && !ops.some((o) => o.worktree === w.id && isActiveOp(o)));

  if (res.error && !res.data) {
    return (
      <>
        <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project }]} />
        <PageHead title={project} />
        <ErrorBanner error={res.error} title={res.error.status === 404 ? t.panel.notFound : t.panel.loadError} />
      </>
    );
  }

  const act = async (w: Worktree, kind: LifecycleKind) => {
    setActionError(undefined);
    announce((kind === "up" ? t.panel.startedMsg : kind === "down" ? t.panel.stoppedMsg : t.panel.restartedMsg)(wtName(w)));
    try {
      await startOperation(project, w.id, kind, kind === "up" ? { auto_ports: true } : {});
    } catch (e) {
      setActionError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
    }
  };

  const pauseAll = async () => {
    setActionError(undefined);
    announce(t.panel.pausedAllMsg(pausable.length));
    for (const w of pausable) {
      try {
        await startOperation(project, w.id, "stop");
      } catch (e) {
        setActionError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
      }
    }
  };

  return (
    <>
      <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project }]} />
      <PageHead
        title={project}
        sub={res.data ? <span class="mono">{res.data.path}</span> : undefined}
        actions={
          <>
            <button type="button" class="btn" onClick={() => { res.reload(); proxy.reload(); onRefreshMeta(); }}><Icon name="restart" /> {t.panel.refresh}</button>
            <button type="button" class="btn" disabled={pausable.length === 0 || res.data?.docker.available === false} title={t.panel.pauseAllHint} aria-describedby="pause-all-hint" onClick={() => void pauseAll()}><Icon name="stop" /> {t.panel.pauseAll}</button>
            <a class="btn" href={href.shared(project)}>{t.shared.link}</a>
            <a class="btn primary" href={href.wizard(project)}><Icon name="plus" /> {t.panel.newBranch}</a>
          </>
        }
      />
      <span id="pause-all-hint" class="sr-only">{t.panel.pauseAllHint}</span>
      {proxy.data ? (
        <p class="small muted">
          {proxy.data.running && proxy.data.url
            ? <ExternalLink href={proxy.data.url} label={t.settingsPage.proxyRoutes}>{t.panel.proxyOn(proxy.data.port)}</ExternalLink>
            : <>{t.panel.proxyOff} · <a href={href.settings()}>{t.panel.proxySettings}</a></>}
        </p>
      ) : null}
      {res.data && !res.data.docker.available ? <Banner tone="warn" title={t.banner.dockerTitle}><p>{res.data.docker.error ?? t.banner.dockerBody}</p><p>{t.panel.unknownStateNote}</p></Banner> : null}
      <ErrorBanner error={actionError} />
      {res.loading && !res.data ? <Loading /> : null}
      {res.data ? (
        <>
          <div class="row">
            <div class="field">
              <label for="wt-filter">{t.panel.filter}</label>
              <input id="wt-filter" class="input" type="search" value={filter} placeholder={t.panel.filterPlaceholder} onInput={(e) => setFilter((e.currentTarget as HTMLInputElement).value)} />
            </div>
            {owners.length > 0 ? (
              <div class="field">
                <label for="wt-owner">{t.panel.ownerFilter}</label>
                <select id="wt-owner" class="input auto" value={owner} onChange={(e) => setOwner((e.currentTarget as HTMLSelectElement).value)}>
                  <option value="">{t.panel.ownerAll}</option>
                  {owners.map((o) => <option key={o} value={o}>{o}</option>)}
                  <option value="-">{t.panel.ownerNone}</option>
                </select>
              </div>
            ) : null}
          </div>
          {wts.length === 0 ? <p class="empty">{t.panel.noWorktrees}</p> : shown.length === 0 ? <p class="empty">{t.panel.noMatch}</p> : (
            <div class="table-wrap" tabIndex={0} role="region" aria-label={t.panel.tableCaption(project)}>
              <table class="data">
                <caption class="sr-only">{t.panel.tableCaption(project)}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t.panel.colWorktree}</th>
                    <th scope="col">{t.panel.colState}</th>
                    <th scope="col">{t.panel.colServices}</th>
                    <th scope="col">{t.panel.colAccess}</th>
                    <th scope="col">{t.panel.colActions}</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((w) => <Row key={w.id} project={project} w={w} act={act} onRemove={() => setRemoving(w)} dockerDown={res.data?.docker.available === false} />)}
                </tbody>
              </table>
            </div>
          )}
        </>
      ) : null}
      {removing ? <RemoveFlow project={project} wt={removing} onClose={() => setRemoving(null)} onDone={() => { setRemoving(null); res.reload(); }} /> : null}
    </>
  );
}

function Row({ project, w, act, onRemove, dockerDown }: { project: string; w: Worktree; act: (w: Worktree, k: LifecycleKind) => void; onRemove: () => void; dockerDown: boolean }) {
  const ops = useOps();
  const op = ops.find((o) => o.worktree === w.id && isActiveOp(o));
  const busyKind = op?.kind ?? w.busy?.kind ?? null;
  const busy = !!busyKind;
  const name = wtName(w);
  const counts = containerCounts(w);
  const view = worktreeStatus(w, busyKind);
  const st = w.state;
  const lock = busy || dockerDown;
  const urls = urlsOf(w);
  // The short form already reaches the default service: its long name is left out here (the detail lists every name).
  const shortFor = (w.proxy_urls ?? []).find((l) => l.default)?.service;
  const domains = (w.proxy_urls ?? []).filter((l) => l.running && (l.default || l.service !== shortFor));
  const conflicts = conflictsOf(w);
  const composeChanged = (w.warnings ?? []).find((x) => x.kind === "compose-changed");
  return (
    <tr aria-busy={busy || undefined}>
      <th scope="row">
        <a href={href.detail(project, w.id)}>{name}</a>
        <div class="small muted mono">{w.path.split(/[\\/]/).filter(Boolean).pop()}</div>
        <div class="row">
          {w.main ? <span class="tag">{t.panel.main}</span> : null}
          {w.detached ? <span class="tag">{t.panel.detached}</span> : null}
          {w.dirty ? <span class="tag"><Icon name="dot" size={10} /> {t.panel.dirty}</span> : null}
          {w.owner ? <span class="tag">{t.panel.owner(w.owner)}</span> : null}
          {composeChanged ? <span class="tag" title={composeChanged.fix ? `${composeChanged.message} (${composeChanged.fix})` : composeChanged.message}><Icon name="warn" size={10} /> {t.panel.composeChanged}</span> : null}
        </div>
      </th>
      <td>
        <Badge view={view} />
        {busy ? <div><button type="button" class="btn sm quiet" onClick={() => window.dispatchEvent(new CustomEvent("theo:show-operations", { cancelable: true }))}>{t.panel.progress}</button></div> : null}
      </td>
      <td class="nowrap">{counts ? t.panel.servicesCount(counts.running, counts.total) : t.panel.servicesNone}</td>
      <td class="url-list">
        {conflicts.length > 0 ? (
          <ul class="plain">{conflicts.map((c, i) => <li key={i}><span class="badge error"><Icon name="portbusy" /> <span>{t.panel.portBusy(c.port)}</span></span> <span class="small">{c.service}</span></li>)}</ul>
        ) : null}
        {domains.length > 0 ? (
          <ul class="plain">{domains.map((u, i) => <li key={"d" + i}><ExternalLink href={u.url} label={u.service}>{u.url.replace(/^https?:\/\//, "")}</ExternalLink> <span class="small muted">{u.default ? "" : u.service}</span></li>)}</ul>
        ) : null}
        {urls.length > 0 ? (
          <ul class={domains.length > 0 ? "plain small" : "plain"}>{urls.map((u, i) => <li key={i}><ExternalLink href={u.url} label={u.service}>{u.url.replace(/^https?:\/\//, "")}</ExternalLink> <span class="small muted">{u.service}</span></li>)}</ul>
        ) : conflicts.length === 0 ? <span class="muted">{t.panel.noUrls}</span> : null}
      </td>
      <td class="actions">
        <div class="row">
          <button type="button" class="btn sm" disabled={lock || st === "running"} onClick={() => act(w, "up")} aria-label={t.panel.startNamed(name)}><Icon name="play" /> {t.panel.start}</button>
          <button type="button" class="btn sm" disabled={lock || st === "absent"} onClick={() => act(w, "down")} aria-label={t.panel.stopNamed(name)}><Icon name="stop" /> {t.panel.stop}</button>
          <button type="button" class="btn sm" disabled={lock || st === "absent" || st === "stopped"} onClick={() => act(w, "restart")} aria-label={t.panel.restartNamed(name)}><Icon name="restart" /> {t.panel.restart}</button>
          <details class="menu">
            <summary class="btn sm" aria-label={t.app.moreFor(name)}>{t.app.more}</summary>
            <div class="menu-list">
              <a class="btn quiet sm" href={href.detail(project, w.id)}>{t.panel.details}<span class="sr-only"> {name}</span></a>
              <a class="btn quiet sm" href={href.detail(project, w.id, "volumes")}>{t.panel.deleteData}<span class="sr-only"> {name}</span></a>
              {!w.main ? <button type="button" class="btn quiet sm danger" onClick={(e) => { (e.currentTarget.closest("details") as HTMLDetailsElement).open = false; onRemove(); }}>{t.panel.removeWorktree}<span class="sr-only"> {name}</span></button> : null}
            </div>
          </details>
        </div>
      </td>
    </tr>
  );
}
