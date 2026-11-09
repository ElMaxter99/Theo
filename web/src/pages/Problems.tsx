// V-08 Problems and cleanup. "Problems": the warnings of the instances (ports, env files, Compose files that changed,
// forgotten worktrees) with what to do, and the differences between Theo's records and Git and Docker. "Cleanup": the
// forgotten work of ADR-0016 (below).
import { useEffect, useState } from "preact/hooks";
import { api, ApiError, projectPath, qs } from "../api/client.ts";
import type { CleanEntry, CleanReport, LifeReport, LifeWorktree } from "../api/types.ts";
import { sizeText } from "../lib/lifecycle.ts";
import { Banner, ErrorBanner, Loading, PageHead } from "../components/ui.tsx";
import { TypedConfirmDialog } from "../components/Dialog.tsx";
import { CopyCommand, theoCmd } from "../components/CopyCommand.tsx";
import { Icon } from "../components/Icon.tsx";
import { useResource } from "../lib/hooks.ts";
import { href } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { refreshStatus, statusName, useStatus } from "../lib/statusStore.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";
import { Health } from "./Health.tsx";
import { Cleanup } from "./Cleanup.tsx";

export function Problems({ tab, project }: { tab: "problems" | "cleanup"; project?: string }) {
  useEffect(() => setTitle([tab === "cleanup" ? t.problems.cleanupTab : t.problems.title]), [tab]);
  return (
    <>
      <PageHead title={t.problems.title} sub={t.problems.lead} />
      <nav class="subnav" aria-label={t.problems.title}>
        <ul>
          <li><a href={href.problems()} aria-current={tab === "problems" ? "page" : undefined}>{t.problems.problemsTab}</a></li>
          <li><a href={href.problems("cleanup")} aria-current={tab === "cleanup" ? "page" : undefined}>{t.problems.cleanupTab}</a></li>
        </ul>
      </nav>
      {tab === "cleanup" ? <Cleanup initial={project} /> : (
        <>
          <InstanceWarnings />
          <h2>{t.problems.recordsTitle}</h2>
          <Health embedded />
        </>
      )}
    </>
  );
}

function InstanceWarnings() {
  const status = useStatus();
  const r = status.report;
  if (!r) return status.loading ? <Loading /> : null;
  const items = r.projects.flatMap((p) => [
    ...p.warnings.map((w) => ({ key: `${p.name}|${w.kind}|${w.message}`, project: p.name, where: p.name, link: href.project(p.name), w })),
    ...p.worktrees.flatMap((wt) => wt.warnings.map((w) => ({ key: `${wt.id}|${w.kind}|${w.message}`, project: p.name, where: `${p.name} › ${statusName(wt)}`, link: href.detail(p.name, wt.id), w }))),
  ]);
  return (
    <section class="stack" aria-labelledby="pb-warn">
      <h2 id="pb-warn">{t.problems.warningsTitle(items.length)}</h2>
      {items.length === 0 ? <Banner tone="ok"><p>{t.problems.noWarnings}</p></Banner> : (
        <ul class="cards">
          {items.map((it) => (
            <li class="card stack-sm" key={it.key}>
              <p class="row"><Icon name="warn" /> <strong>{t.problems.kind[it.w.kind] ?? it.w.kind}</strong> <a href={it.link} class="small">{it.where}</a></p>
              <p>{it.w.message}</p>
              {it.w.fix ? (
                <p class="small">{t.problems.fix} <code>{it.w.fix}</code> {/^theo /.test(it.w.fix) ? <CopyCommand command={it.w.fix} /> : null}</p>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
