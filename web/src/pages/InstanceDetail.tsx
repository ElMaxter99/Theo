import { useEffect } from "preact/hooks";
import { api, ApiError, wtPath } from "../api/client.ts";
import type { WorktreeDetail } from "../api/types.ts";
import { Badge, Banner, Crumbs, ErrorBanner, Loading, PageHead } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { Tabs } from "../components/Tabs.tsx";
import { useResource } from "../lib/hooks.ts";
import { href, navigate, TABS } from "../lib/router.ts";
import type { TabId } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { isActiveOp, opErrorText, worktreeStatus } from "../lib/status.ts";
import { rememberWorktrees, startOperation, useOps } from "../ops/store.ts";
import type { LifecycleKind } from "../ops/store.ts";
import { useState } from "preact/hooks";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";
import { wtName } from "./ProjectPanel.tsx";
import { SummaryTab } from "./tabs/SummaryTab.tsx";
import { ServicesTab } from "./tabs/ServicesTab.tsx";
import { PortsTab } from "./tabs/PortsTab.tsx";
import { LogsTab } from "./tabs/LogsTab.tsx";
import { VariablesTab } from "./tabs/VariablesTab.tsx";
import { VolumesTab } from "./tabs/VolumesTab.tsx";
import { HistoryTab } from "./tabs/HistoryTab.tsx";

const labels: Record<TabId, string> = {
  summary: t.detail.tabSummary,
  services: t.detail.tabServices,
  ports: t.detail.tabPorts,
  logs: t.detail.tabLogs,
  variables: t.detail.tabVariables,
  volumes: t.detail.tabVolumes,
  history: t.detail.tabHistory,
};

export function InstanceDetail({ project, wt, tab }: { project: string; wt: string; tab: TabId }) {
  const res = useResource<WorktreeDetail>(() => api.get<WorktreeDetail>(wtPath(project, wt)), [project, wt], { project, worktree: wt });
  const ops = useOps();
  const [actionError, setActionError] = useState<ApiError | undefined>();
  const w = res.data;
  useEffect(() => { if (w) { rememberWorktrees([w]); setTitle([wtName(w), project]); } }, [w, project]);
  const activeKey = ops.filter((o) => o.worktree === wt).map((o) => o.id + o.state).join(",");
  useEffect(() => { if (w) res.reload(); /* eslint-disable-next-line */ }, [activeKey]);

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
  const act = async (kind: LifecycleKind) => {
    setActionError(undefined);
    announce(`${kind} ${name}`);
    try { await startOperation(project, wt, kind, kind === "up" ? { auto_ports: true } : {}); }
    catch (e) { setActionError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) })); }
  };
  const st = w.state;
  const lock = !!busyKind;

  return (
    <>
      <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project, href: href.project(project) }, { label: name }]} />
      <PageHead
        title={name}
        sub={<span class="row"><Badge view={worktreeStatus(w, busyKind)} /><span class="mono small">{w.path}</span></span>}
        actions={
          <>
            <button type="button" class="btn" disabled={lock || st === "running"} onClick={() => act("up")}><Icon name="play" /> {t.panel.start}</button>
            <button type="button" class="btn" disabled={lock || st === "absent"} onClick={() => act("down")}><Icon name="stop" /> {t.panel.stop}</button>
            <button type="button" class="btn" disabled={lock || st === "absent" || st === "stopped"} onClick={() => act("restart")}><Icon name="restart" /> {t.panel.restart}</button>
          </>
        }
      />
      <ErrorBanner error={actionError} />
      {lastFailed && lastFailed.state === "failed" ? (
        <Banner tone="error" title={t.detail.lastFailed}>
          {opErrorText(lastFailed) ? <p>{opErrorText(lastFailed)}</p> : null}
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
        {tab === "summary" ? <SummaryTab project={project} w={w} /> : null}
        {tab === "services" ? <ServicesTab project={project} w={w} /> : null}
        {tab === "ports" ? <PortsTab project={project} w={w} /> : null}
        {tab === "logs" ? <LogsTab project={project} w={w} /> : null}
        {tab === "variables" ? <VariablesTab project={project} w={w} /> : null}
        {tab === "volumes" ? <VolumesTab project={project} w={w} /> : null}
        {tab === "history" ? <HistoryTab project={project} w={w} /> : null}
      </Tabs>
    </>
  );
}
