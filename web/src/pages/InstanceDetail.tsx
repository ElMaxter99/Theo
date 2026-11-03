// V-05 Instance: five tabs instead of seven. Summary has the services (with their own actions), the ports and the
// addresses; Configuration has the variables, the profiles and the settings; Data has the volumes, backups and the
// deletion; Activity is the history. The action that fits the state is the main button; the rest is in the menu.
import { useEffect, useState } from "preact/hooks";
import { api, ApiError, wtPath } from "../api/client.ts";
import type { WorktreeDetail } from "../api/types.ts";
import { Badge, Banner, Crumbs, ErrorBanner, ExternalLink, Loading, PageHead } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { MenuButton } from "../components/Menu.tsx";
import { copyText, theoCmd } from "../components/CopyCommand.tsx";
import { RemoveFlow } from "../components/RemoveFlow.tsx";
import { Tabs } from "../components/Tabs.tsx";
import { useResource } from "../lib/hooks.ts";
import { href, navigate, TABS } from "../lib/router.ts";
import type { TabId } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { isActiveOp, opErrorOutput, opErrorText, worktreeStatus } from "../lib/status.ts";
import { rememberWorktrees, startOperation, useOps } from "../ops/store.ts";
import type { LifecycleKind } from "../ops/store.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";
import { wtArg, wtName } from "./ProjectPanel.tsx";
import { SummaryTab } from "./tabs/SummaryTab.tsx";
import { LogsTab } from "./tabs/LogsTab.tsx";
import { ConfigTab } from "./tabs/ConfigTab.tsx";
import { DataTab } from "./tabs/DataTab.tsx";
import { HistoryTab } from "./tabs/HistoryTab.tsx";

const ACTIVE = ["running", "partial", "unhealthy"];

export function InstanceDetail({ project, wt, tab }: { project: string; wt: string; tab: TabId }) {
  const res = useResource<WorktreeDetail>(() => api.get<WorktreeDetail>(wtPath(project, wt)), [project, wt], { project, worktree: wt });
  const ops = useOps();
  const [actionError, setActionError] = useState<ApiError | undefined>();
  const [removing, setRemoving] = useState(false);
  const w = res.data;
  useEffect(() => { if (w) { rememberWorktrees([w]); setTitle([wtName(w), project]); } }, [w, project]);
  const activeKey = ops.filter((o) => o.worktree === wt).map((o) => o.id + o.state).join(",");
  useEffect(() => { if (w) res.reload(); /* eslint-disable-next-line */ }, [activeKey]);

  const labels: Record<TabId, string> = {
    summary: t.detail.tabSummary, logs: t.detail.tabLogs, config: t.detail.tabConfig, data: t.detail.tabData, activity: t.detail.tabActivity,
  };

  if (res.error && !w) {
    return (
      <>
        <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project, href: href.project(project) }]} />
        <PageHead title={t.detail.notFound.replace(/\.$/, "")} />
        <ErrorBanner error={res.error} title={res.error.status === 404 ? t.detail.notFound : t.detail.loadError} />
      </>
    );
  }
  if (!w) return <><PageHead title={t.app.loading} /><Loading /></>;

  const op = ops.find((o) => o.worktree === wt && isActiveOp(o));
  const busyKind = op?.kind ?? w.busy?.kind ?? null;
  const name = wtName(w);
  const lastFailed = ops.find((o) => o.worktree === wt && !isActiveOp(o));
  const act = async (kind: LifecycleKind, body: Record<string, unknown> = {}) => {
    setActionError(undefined);
    announce((kind === "up" ? t.panel.startedMsg : kind === "down" ? t.panel.stoppedMsg : kind === "stop" ? t.panel.pausedMsg : t.panel.restartedMsg)(name));
    try { await startOperation(project, wt, kind, kind === "up" ? { auto_ports: true, ...body } : body); }
    catch (e) { setActionError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) })); }
  };
  const st = w.state;
  const lock = !!busyKind;
  const active = ACTIVE.includes(st);
  const arg = wtArg(w);
  const cmd = (verb: string) => theoCmd(verb, project, arg ? "-w" : "", arg);
  const copy = (c: string) => { void copyText(c).then((ok) => announce(ok ? t.copy.copied : t.copy.failed)); };
  const url = (w.proxy_urls ?? []).find((l) => l.running && l.default)?.url ?? (w.ports ?? []).find((p) => p.url && p.status === "running")?.url;

  return (
    <>
      <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project, href: href.project(project) }, { label: name }]} />
      <PageHead
        title={name}
        sub={<span class="row"><Badge view={worktreeStatus(w, busyKind)} />{url && active ? <ExternalLink href={url} label={name}>{url.replace(/^https?:\/\//, "")}</ExternalLink> : null}<span class="mono small">{w.path}</span></span>}
        actions={
          <>
            {active
              ? <button type="button" class="btn" disabled={lock} onClick={() => act("stop")}><Icon name="stop" /> {t.panel.pause}</button>
              : <button type="button" class="btn primary" disabled={lock} onClick={() => act("up")}><Icon name="play" /> {t.panel.start}</button>}
            {active ? <button type="button" class="btn" disabled={lock} onClick={() => act("restart")}><Icon name="restart" /> {t.panel.restart}</button> : null}
            <MenuButton label={t.app.moreFor(name)} class="btn" items={[
              st !== "absent" ? { label: t.instances.down, disabled: lock, onSelect: () => act("down") } : null,
              st !== "absent" ? { label: t.instances.recreate, disabled: lock, onSelect: () => act("recreate") } : null,
              { label: t.instances.rebuild, disabled: lock, onSelect: () => act("up", { build: true }) },
              { label: t.copy.upCommand, onSelect: () => copy(cmd("up")) },
              { label: t.copy.logsCommand, onSelect: () => copy(`${cmd("logs")} -f`) },
              { label: t.copy.shellCommand, onSelect: () => copy(`${cmd("shell")} <service>`) },
              !w.main ? { label: t.panel.removeWorktree, danger: true, onSelect: () => setRemoving(true) } : null,
            ]} />
          </>
        }
      />
      <ErrorBanner error={actionError} />
      {lastFailed && lastFailed.state === "failed" ? (
        <Banner tone="error" title={t.detail.lastFailed}>
          {opErrorText(lastFailed) ? <p>{opErrorText(lastFailed)}</p> : null}
          {opErrorOutput(lastFailed) ? <details open><summary>{t.banner.detailsOriginal}</summary><pre class="mono small prewrap">{opErrorOutput(lastFailed)}</pre></details> : null}
          <p>{t.detail.keptNote}</p>
        </Banner>
      ) : null}
      <Tabs
        tabs={TABS.map((id) => ({ id, label: labels[id] }))}
        selected={tab}
        onSelect={(id) => navigate(href.detail(project, wt, id as TabId), true)}
        label={t.detail.tabs}
        idBase="inst"
      >
        {tab === "summary" ? <SummaryTab project={project} w={w} lock={lock} /> : null}
        {tab === "logs" ? <LogsTab project={project} w={w} /> : null}
        {tab === "config" ? <ConfigTab project={project} w={w} /> : null}
        {tab === "data" ? <DataTab project={project} w={w} /> : null}
        {tab === "activity" ? <HistoryTab project={project} w={w} /> : null}
      </Tabs>
      {removing ? <RemoveFlow project={project} wt={w} onClose={() => setRemoving(false)} onDone={() => { setRemoving(false); navigate(href.project(project)); }} /> : null}
    </>
  );
}
