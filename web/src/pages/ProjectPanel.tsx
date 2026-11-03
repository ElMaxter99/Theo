// V-03 Project › Instances: one line per worktree with its state, its address and the action that fits it; the rest is
// in the row menu. Filters by state, owner and text; several instances can be started or paused at once.
// With #/p/{p}/new the "New instance" panel (V-04) is open on top.
import { useEffect, useMemo, useState } from "preact/hooks";
import { api, ApiError, projectPath } from "../api/client.ts";
import type { Overview, Worktree } from "../api/types.ts";
import { Badge, Banner, ErrorBanner, ExternalLink, Loading } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { MenuButton } from "../components/Menu.tsx";
import { copyText, theoCmd } from "../components/CopyCommand.tsx";
import { RemoveFlow } from "../components/RemoveFlow.tsx";
import { useResource } from "../lib/hooks.ts";
import { href, navigate } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { conflictsOf, urlsOf } from "../lib/overview.ts";
import { containerCounts, isActiveOp, worktreeStatus } from "../lib/status.ts";
import { rememberWorktrees, startOperation, stopAll, useOps } from "../ops/store.ts";
import type { LifecycleKind } from "../ops/store.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";
import { ProjectHeader } from "./ProjectHeader.tsx";
import { NewInstance } from "./NewInstance.tsx";

export function wtName(w: Pick<Worktree, "branch" | "detached" | "path" | "id">): string {
  return w.branch || (w.detached ? t.panel.detached : "") || w.path.split(/[\\/]/).filter(Boolean).pop() || w.id;
}

/** `-w` argument of the CLI for a worktree: the main one needs none. */
export function wtArg(w: Pick<Worktree, "branch" | "path" | "main">): string | undefined {
  if (w.main) return undefined;
  return w.branch || w.path;
}

type Chip = "all" | "running" | "stopped" | "problems";
const ACTIVE = ["running", "partial", "unhealthy"];
const chipOf = (w: Worktree): Chip => (w.state === "partial" || w.state === "unhealthy" || (w.warnings ?? []).length > 0 && w.state !== "running" ? "problems" : ACTIVE.includes(w.state) ? "running" : "stopped");

const asError = (e: unknown) => (e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));

export function ProjectPanel({ project, drawer, onRefreshMeta }: { project: string; drawer: boolean; onRefreshMeta: () => void }) {
  const res = useResource<Overview>(() => api.get<Overview>(`${projectPath(project)}/overview`), [project], { project });
  const ops = useOps();
  const [filter, setFilter] = useState("");
  const [owner, setOwner] = useState("");
  const [chip, setChip] = useState<Chip>("all");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [removing, setRemoving] = useState<Worktree | null>(null);
  const [actionError, setActionError] = useState<ApiError | undefined>();
  useEffect(() => setTitle([project]), [project]);
  useEffect(() => { if (res.data) rememberWorktrees(res.data.worktrees); }, [res.data]);
  useEffect(() => { setPicked(new Set()); setChip("all"); setFilter(""); setOwner(""); }, [project]);

  // When an operation of this project finishes, the list is refreshed (the server also emits state.changed).
  const activeKey = ops.filter(isActiveOp).map((o) => o.id).join(",");
  useEffect(() => { if (res.data) res.reload(); /* eslint-disable-next-line */ }, [activeKey]);

  const wts = res.data?.worktrees ?? [];
  const owners = useMemo(() => [...new Set(wts.map((w) => w.owner).filter((o): o is string => !!o))].sort(), [wts]);
  const counts = useMemo(() => {
    const c: Record<Chip, number> = { all: wts.length, running: 0, stopped: 0, problems: 0 };
    for (const w of wts) c[chipOf(w)]++;
    return c;
  }, [wts]);
  const shown = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return wts.filter((w) => {
      if (chip !== "all" && chipOf(w) !== chip) return false;
      if (owner === "-" ? !!w.owner : owner && w.owner !== owner) return false;
      if (!q) return true;
      return (w.branch ?? "").toLowerCase().includes(q) || w.path.toLowerCase().includes(q) || (w.owner ?? "").toLowerCase().includes(q);
    });
  }, [wts, filter, owner, chip]);
  const busyIds = new Set(ops.filter(isActiveOp).map((o) => o.worktree));
  const selected = wts.filter((w) => picked.has(w.id));
  const dockerDown = res.data?.docker.available === false;

  const act = async (w: Worktree, kind: LifecycleKind) => {
    setActionError(undefined);
    announce((kind === "up" ? t.panel.startedMsg : kind === "down" ? t.panel.stoppedMsg : kind === "stop" ? t.panel.pausedMsg : t.panel.restartedMsg)(wtName(w)));
    try {
      await startOperation(project, w.id, kind, kind === "up" ? { auto_ports: true } : {});
    } catch (e) {
      setActionError(asError(e));
    }
  };
  const bulk = async (kind: "up" | "stop") => {
    setActionError(undefined);
    const list = selected.filter((w) => !busyIds.has(w.id) && (kind === "up" ? !ACTIVE.includes(w.state) : ACTIVE.includes(w.state)));
    announce(kind === "up" ? t.instances.startingN(list.length) : t.panel.pausedAllMsg(list.length));
    for (const w of list) {
      try { await startOperation(project, w.id, kind, kind === "up" ? { auto_ports: true } : {}); }
      catch (e) { setActionError(asError(e)); }
    }
    setPicked(new Set());
  };
  const toggle = (id: string) => { const n = new Set(picked); if (n.has(id)) n.delete(id); else n.add(id); setPicked(n); };
  const allShown = shown.length > 0 && shown.every((w) => picked.has(w.id));
  const closeDrawer = () => navigate(href.project(project));

  if (res.error && !res.data) {
    return (
      <>
        <ProjectHeader project={project} tab="instances" />
        <ErrorBanner error={res.error} title={res.error.status === 404 ? t.panel.notFound : t.panel.loadError} />
      </>
    );
  }

  return (
    <>
      <ProjectHeader project={project} path={res.data?.path} tab="instances" menu={[
        { label: t.instances.pauseAll, disabled: counts.running + counts.problems === 0 || dockerDown, onSelect: () => { void stopAll(project).catch((e) => setActionError(asError(e))); announce(t.home.stoppingAll); } },
        { label: t.app.refresh, onSelect: () => { res.reload(); onRefreshMeta(); } },
      ]} />
      {dockerDown ? <Banner tone="warn" title={t.banner.dockerTitle}><p>{res.data?.docker.error ?? t.banner.dockerBody}</p><p>{t.panel.unknownStateNote}</p></Banner> : null}
      <ErrorBanner error={actionError} />
      {res.loading && !res.data ? <Loading /> : null}
      {res.data ? (
        <>
          <div class="toolbar">
            <div class="chips" role="group" aria-label={t.instances.filterState}>
              {(["all", "running", "stopped", "problems"] as Chip[]).map((c) => (
                <button key={c} type="button" class="chip" aria-pressed={chip === c} onClick={() => setChip(c)}>{t.instances.chip[c]} <span class="chip-n">{counts[c]}</span></button>
              ))}
            </div>
            <label class="sr-only" for="wt-filter">{t.panel.filter}</label>
            <input id="wt-filter" class="input search" type="search" value={filter} placeholder={t.panel.filterPlaceholder} onInput={(e) => setFilter((e.currentTarget as HTMLInputElement).value)} />
            {owners.length > 0 ? (
              <>
                <label class="sr-only" for="wt-owner">{t.panel.ownerFilter}</label>
                <select id="wt-owner" class="input auto" value={owner} onChange={(e) => setOwner((e.currentTarget as HTMLSelectElement).value)}>
                  <option value="">{t.panel.ownerAll}</option>
                  {owners.map((o) => <option key={o} value={o}>{o}</option>)}
                  <option value="-">{t.panel.ownerNone}</option>
                </select>
              </>
            ) : null}
          </div>
          {selected.length > 0 ? (
            <div class="bulkbar" role="region" aria-label={t.instances.selection(selected.length)}>
              <strong>{t.instances.selection(selected.length)}</strong>
              <button type="button" class="btn sm" disabled={dockerDown} onClick={() => void bulk("up")}><Icon name="play" /> {t.panel.start}</button>
              <button type="button" class="btn sm" disabled={dockerDown} onClick={() => void bulk("stop")}><Icon name="stop" /> {t.panel.pause}</button>
              <button type="button" class="btn sm quiet" onClick={() => setPicked(new Set())}>{t.instances.clearSelection}</button>
            </div>
          ) : null}
          {wts.length === 0 ? <p class="empty">{t.panel.noWorktrees}</p> : shown.length === 0 ? <p class="empty">{t.panel.noMatch}</p> : (
            <div class="table-wrap" tabIndex={0} role="region" aria-label={t.panel.tableCaption(project)}>
              <table class="data rows">
                <caption class="sr-only">{t.panel.tableCaption(project)}</caption>
                <thead>
                  <tr>
                    <th scope="col" class="pick">
                      <input type="checkbox" aria-label={t.instances.selectAll} checked={allShown}
                        onChange={(e) => { const on = (e.currentTarget as HTMLInputElement).checked; const n = new Set(picked); for (const w of shown) { if (on) n.add(w.id); else n.delete(w.id); } setPicked(n); }} />
                    </th>
                    <th scope="col">{t.panel.colWorktree}</th>
                    <th scope="col">{t.panel.colState}</th>
                    <th scope="col">{t.panel.colAccess}</th>
                    <th scope="col" class="num">{t.panel.colActions}</th>
                  </tr>
                </thead>
                <tbody>
                  {shown.map((w) => <Row key={w.id} project={project} w={w} act={act} picked={picked.has(w.id)} onPick={() => toggle(w.id)} onRemove={() => setRemoving(w)} dockerDown={dockerDown} />)}
                </tbody>
              </table>
            </div>
          )}
        </>
      ) : null}
      {removing ? <RemoveFlow project={project} wt={removing} onClose={() => setRemoving(null)} onDone={() => { setRemoving(null); res.reload(); }} /> : null}
      {drawer ? <NewInstance project={project} overview={res.data} onClose={closeDrawer} /> : null}
    </>
  );
}

function Row({ project, w, act, picked, onPick, onRemove, dockerDown }: {
  project: string; w: Worktree; act: (w: Worktree, k: LifecycleKind) => void; picked: boolean; onPick: () => void; onRemove: () => void; dockerDown: boolean;
}) {
  const ops = useOps();
  const op = ops.find((o) => o.worktree === w.id && isActiveOp(o));
  const busyKind = op?.kind ?? w.busy?.kind ?? null;
  const busy = !!busyKind;
  const name = wtName(w);
  const counts = containerCounts(w);
  const view = worktreeStatus(w, busyKind);
  const st = w.state;
  const lock = busy || dockerDown;
  const domains = (w.proxy_urls ?? []).filter((l) => l.running);
  const first = domains.find((l) => l.default) ?? domains[0];
  const url = first?.url ?? urlsOf(w)[0]?.url;
  const conflicts = conflictsOf(w);
  const composeChanged = (w.warnings ?? []).find((x) => x.kind === "compose-changed");
  const active = ACTIVE.includes(st);
  const failing = st === "partial" || st === "unhealthy";
  const arg = wtArg(w);
  const copy = (cmd: string) => { void copyText(cmd).then((ok) => announce(ok ? t.copy.copied : t.copy.failed)); };

  let primary;
  if (busy) primary = <button type="button" class="btn sm quiet" onClick={() => window.dispatchEvent(new CustomEvent("theo:show-operations", { cancelable: true }))}>{t.panel.progress}</button>;
  else if (failing) primary = <a class="btn sm" href={href.detail(project, w.id, "logs")}>{t.instances.seeLogs}<span class="sr-only"> {name}</span></a>;
  else if (active && url) primary = <a class="btn sm" href={url} target="_blank" rel="noopener noreferrer">{t.instances.open}<span class="sr-only"> {name} ({t.ports.newTab})</span> <Icon name="external" size={13} /></a>;
  else if (active) primary = <button type="button" class="btn sm" disabled={lock} onClick={() => act(w, "stop")} aria-label={t.panel.pauseNamed(name)}><Icon name="stop" /> {t.panel.pause}</button>;
  else primary = <button type="button" class="btn sm primary" disabled={lock} onClick={() => act(w, "up")} aria-label={t.panel.startNamed(name)}><Icon name="play" /> {t.panel.start}</button>;

  return (
    <tr aria-busy={busy || undefined} class={picked ? "picked" : undefined}>
      <td class="pick"><input type="checkbox" checked={picked} aria-label={t.instances.select(name)} onChange={onPick} /></td>
      <th scope="row">
        <span class="row tight">
          <a href={href.detail(project, w.id)}>{name}</a>
          {w.main ? <span class="tag">{t.panel.main}</span> : null}
          {w.dirty ? <span class="tag" title={t.panel.dirty}><Icon name="dot" size={10} /> {t.panel.dirty}</span> : null}
          {w.owner ? <span class="tag">{t.panel.owner(w.owner)}</span> : null}
          {composeChanged ? <span class="tag" title={composeChanged.fix ? `${composeChanged.message} (${composeChanged.fix})` : composeChanged.message}><Icon name="warn" size={10} /> {t.panel.composeChanged}</span> : null}
        </span>
      </th>
      <td class="nowrap"><Badge view={view} /> {counts && counts.total > 0 ? <span class="small muted">{t.panel.servicesCount(counts.running, counts.total)}</span> : null}</td>
      <td class="url-list">
        {conflicts.length > 0 ? <span class="badge error"><Icon name="portbusy" /> <span>{t.panel.portBusy(conflicts[0]!.port)}</span></span>
          : url && active ? <ExternalLink href={url} label={name}>{url.replace(/^https?:\/\//, "")}</ExternalLink>
          : <span class="muted">—</span>}
      </td>
      <td class="actions num">
        <span class="row end">
          {primary}
          <MenuButton label={t.app.moreFor(name)} testid={`menu-${w.id}`} items={[
            !active ? { label: t.panel.start, disabled: lock, onSelect: () => act(w, "up") } : null,
            active ? { label: t.panel.pause, disabled: lock, onSelect: () => act(w, "stop") } : null,
            st !== "absent" ? { label: t.panel.restart, disabled: lock || st === "stopped", onSelect: () => act(w, "restart") } : null,
            st !== "absent" ? { label: t.instances.down, disabled: lock, onSelect: () => act(w, "down") } : null,
            { label: t.panel.details, href: href.detail(project, w.id), sr: name },
            { label: t.detail.tabLogs, href: href.detail(project, w.id, "logs"), sr: name },
            { label: t.copy.upCommand, onSelect: () => copy(theoCmd("up", project, arg ? "-w" : "", arg)) },
            { label: t.panel.deleteData, href: href.detail(project, w.id, "data"), sr: name },
            !w.main ? { label: t.panel.removeWorktree, danger: true, onSelect: onRemove, sr: name } : null,
          ]} />
        </span>
      </td>
    </tr>
  );
}
