// V-01 Home: what runs now in every project, what needs attention, and what happened last. With no project yet it is
// the welcome screen (V-10) with the checks of Git and Docker.
import { useEffect, useMemo } from "preact/hooks";
import { getWithHeaders, qs } from "../api/client.ts";
import type { HistoryEvent, Meta, StatusWorktree } from "../api/types.ts";
import { Badge, Banner, ErrorBanner, ExternalLink, Loading, PageHead } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { CopyCommand, theoCmd } from "../components/CopyCommand.tsx";
import { brand } from "../brand.ts";
import { useAction, useResource } from "../lib/hooks.ts";
import { formatTime } from "../lib/format.ts";
import { useHealth } from "../lib/health.ts";
import { href } from "../lib/router.ts";
import { stateView } from "../lib/status.ts";
import { forgotten } from "../lib/lifecycle.ts";
import { ACTIVE, statusName, statusUrl, useStatus, warningCount, wtState } from "../lib/statusStore.ts";
import { startOperation, stopAll } from "../ops/store.ts";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function Home({ meta }: { meta: Meta | undefined }) {
  const status = useStatus();
  const health = useHealth();
  const recent = useResource<HistoryEvent[]>(async () => (await getWithHeaders<HistoryEvent[]>(`/history${qs({ limit: 8 })}`)).data, [], { project: "*" });
  useEffect(() => setTitle([t.home.title]), []);
  const report = status.report;

  const running = useMemo(() => {
    const out: { project: string; w: StatusWorktree }[] = [];
    for (const p of report?.projects ?? []) for (const w of p.worktrees) if (ACTIVE.includes(wtState(w))) out.push({ project: p.name, w });
    return out;
  }, [report]);
  const stop = useAction(async () => { await stopAll(); announce(t.home.stoppingAll); });
  const stopOne = useAction(async (project: string, id: string) => { await startOperation(project, id, "stop"); });

  if (!report && status.loading) return <><PageHead title={t.home.title} /><Loading /></>;
  if (!report && status.error) return <><PageHead title={t.home.title} /><ErrorBanner error={status.error} /></>;
  if (report && report.projects.length === 0) return <Welcome meta={meta} />;

  const findings = health.summary?.findings ?? 0;
  const warnings = warningCount(report);
  const attention = (report?.projects ?? []).flatMap((p) => p.worktrees.filter((w) => wtState(w) === "partial" || wtState(w) === "unhealthy").map((w) => ({ project: p.name, w })));

  return (
    <>
      <PageHead
        title={t.home.title}
        sub={t.home.summary(running.length, report?.projects.length ?? 0)}
        actions={
          <>
            <button type="button" class="btn" disabled={running.length === 0 || stop.busy} onClick={() => void stop.run()}><Icon name="stop" /> {t.home.stopAll}</button>
            <CopyCommand command={theoCmd("stop", "--all")} />
          </>
        }
      />
      <ErrorBanner error={stop.error ?? stopOne.error} />
      {report?.docker_error ? <Banner tone="warn" title={t.banner.dockerTitle}><p>{report.docker_error}</p></Banner> : null}

      <section class="card stack" aria-labelledby="home-running">
        <h2 id="home-running">{t.home.runningTitle}</h2>
        {running.length === 0 ? <p class="muted">{t.home.nothingRunning}</p> : (
          <ul class="list-rows">
            {running.map(({ project, w }) => {
              const url = statusUrl(w);
              const st = wtState(w);
              return (
                <li key={w.id} class="list-row">
                  <Badge view={stateView(st, w.instance?.counts)} />
                  <a href={href.detail(project, w.id)} class="grow"><strong>{statusName(w)}</strong> <span class="muted">· {project}</span></a>
                  {url ? <ExternalLink href={url} label={statusName(w)}>{url.replace(/^https?:\/\//, "")}</ExternalLink> : null}
                  <button type="button" class="btn sm" disabled={stopOne.busy} onClick={() => void stopOne.run(project, w.id)} aria-label={t.panel.pauseNamed(statusName(w))}><Icon name="stop" /> {t.panel.pause}</button>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      <div class="grid-2">
        <section class="card stack" aria-labelledby="home-attention">
          <h2 id="home-attention">{t.home.attentionTitle}</h2>
          {attention.length === 0 && findings === 0 && warnings === 0 ? <p class="muted">{t.home.allGood}</p> : null}
          {attention.length > 0 ? (
            <ul class="plain">
              {attention.map(({ project, w }) => (
                <li key={w.id}><Badge view={stateView(wtState(w), w.instance?.counts)} /> <a href={href.detail(project, w.id, "logs")}>{project} › {statusName(w)}</a>{w.warnings[0] ? <span class="small muted"> — {w.warnings[0].message}</span> : null}</li>
              ))}
            </ul>
          ) : null}
          {findings > 0 || warnings > 0 ? <p><a href={href.problems()}>{t.home.seeProblems(findings + warnings)}</a></p> : null}
        </section>

        {(() => {
          const f = forgotten(report?.projects ?? []);
          const n = f.close + f.inactive;
          if (n === 0 && f.idle === 0) return null;
          return (
            <section class="card stack" aria-labelledby="home-forgotten">
              <h2 id="home-forgotten">{t.life.forgottenTitle}</h2>
              <p class="h1">{n}</p>
              <p class="small muted">{t.life.forgottenDetail(f.close, f.inactive, f.idle)}</p>
              <p><a href={href.problems("cleanup")}>{t.life.reviewCleanup}</a></p>
            </section>
          );
        })()}

        <section class="card stack" aria-labelledby="home-recent">
          <h2 id="home-recent">{t.home.recentTitle}</h2>
          {recent.loading && !recent.data ? <Loading /> : null}
          {recent.data && recent.data.length === 0 ? <p class="muted">{t.home.noActivity}</p> : null}
          {recent.data && recent.data.length > 0 ? (
            <ul class="plain">
              {[...recent.data].reverse().map((e, i) => (
                <li key={i} class="small">
                  <Icon name={e.result === "ok" ? "ok" : "error"} /> <strong>{e.operation}</strong> {e.project ? <a href={href.project(e.project)}>{e.project}</a> : null}
                  {e.compose_project ? <span class="mono muted"> {e.compose_project}</span> : null} <span class="muted">· {formatTime(e.time)}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </section>
      </div>

      <section class="stack" aria-labelledby="home-projects">
        <h2 id="home-projects">{t.home.quickTitle}</h2>
        <div class="row">
          {(report?.projects ?? []).map((p) => (
            <a key={p.name} class="btn" href={href.wizard(p.name)}><Icon name="plus" /> {t.home.newIn(p.name)}</a>
          ))}
          <a class="btn quiet" href={href.add()}>{t.projects.add}</a>
        </div>
      </section>
    </>
  );
}

function Welcome({ meta }: { meta: Meta | undefined }) {
  useEffect(() => setTitle([t.welcome.title]), []);
  const check = (ok: boolean | undefined, label: string) => (
    <li class="row"><Icon name={ok === undefined ? "dot" : ok ? "ok" : "error"} /> <span>{label}</span></li>
  );
  return (
    <div class="welcome">
      <img class="mascot" src={brand.mascot} alt={t.welcome.mascotAlt} />
      <div class="stack">
        <h1 tabIndex={-1}>{t.welcome.title}</h1>
        <p class="lead">{t.welcome.lead}</p>
        <div class="card">
          <h2 class="h3">{t.welcome.checksTitle}</h2>
          <ul class="plain stack-sm">
            {check(meta?.git.available, meta?.git.available ? t.welcome.gitOk(meta.git.version ?? "") : t.welcome.gitMissing)}
            {check(meta?.docker.available, meta?.docker.available ? t.welcome.dockerOk(meta.docker.version ?? "", meta.compose?.version ?? "") : t.welcome.dockerMissing)}
            {check(meta ? !!meta.data_dir : undefined, t.welcome.dataDir)}
          </ul>
        </div>
        <p class="row">
          <a class="btn primary" href={href.add()}>{t.welcome.add}</a>
          <a class="btn" href={href.settings("diagnostics")}>{t.welcome.diagnostics}</a>
        </p>
        <p class="small muted">{t.welcome.cli} <code>{theoCmd("project", "add", ".")}</code> <CopyCommand command={theoCmd("project", "add", ".")} /></p>
        <details>
          <summary>{t.welcome.how}</summary>
          <ol>
            <li>{t.welcome.step1}</li>
            <li>{t.welcome.step2}</li>
            <li>{t.welcome.step3}</li>
          </ol>
          <p class="muted">{t.welcome.note}</p>
        </details>
      </div>
    </div>
  );
}
