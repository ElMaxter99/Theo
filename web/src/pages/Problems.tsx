// V-08 Problems and cleanup. "Problems": the warnings of the instances (ports, env files, Compose files that changed)
// with what to do, and the differences between Theo's records and Git and Docker. "Cleanup": the worktrees whose
// branch is already merged, removed only after typing the project name.
import { useEffect, useState } from "preact/hooks";
import { api, projectPath, qs } from "../api/client.ts";
import type { PruneReport } from "../api/types.ts";
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

export function Problems({ tab }: { tab: "problems" | "cleanup" }) {
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
      {tab === "cleanup" ? <Cleanup /> : (
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

function Cleanup() {
  const status = useStatus();
  const names = (status.report?.projects ?? []).map((p) => p.name);
  const [project, setProject] = useState("");
  const chosen = project || names[0] || "";
  const res = useResource<PruneReport | null>(() => (chosen ? api.get<PruneReport>(`${projectPath(chosen)}/prune${qs({})}`) : Promise.resolve(null)), [chosen], { project: chosen });
  const [confirming, setConfirming] = useState(false);
  const c = t.cleanup;
  const removable = (res.data?.merged ?? []).filter((m) => !m.reason);
  const staying = (res.data?.merged ?? []).filter((m) => m.reason);
  return (
    <section class="stack" aria-labelledby="cl-title">
      <h2 id="cl-title">{c.title}</h2>
      <p>{c.lead}</p>
      {names.length > 1 ? (
        <div class="field">
          <label for="cl-project">{c.project}</label>
          <select id="cl-project" class="input auto" value={chosen} onChange={(e) => setProject((e.currentTarget as HTMLSelectElement).value)}>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </div>
      ) : null}
      {res.loading && !res.data ? <Loading /> : null}
      <ErrorBanner error={res.error} />
      {res.data ? (
        <>
          <p class="muted">{c.summary(res.data.into, removable.length, res.data.not_merged)}</p>
          {removable.length === 0 ? <Banner tone="ok"><p>{c.nothing}</p></Banner> : (
            <div class="table-wrap" tabIndex={0} role="region" aria-label={c.removable}>
              <table class="data">
                <caption class="sr-only">{c.removable}</caption>
                <thead><tr><th scope="col">{t.panel.colWorktree}</th><th scope="col">{c.folder}</th></tr></thead>
                <tbody>{removable.map((m) => <tr key={m.path}><th scope="row">{m.branch}</th><td class="mono small">{m.path}</td></tr>)}</tbody>
              </table>
            </div>
          )}
          {staying.length > 0 ? (
            <details>
              <summary>{c.staying(staying.length)}</summary>
              <ul>{staying.map((m) => <li key={m.path}><span class="mono">{m.branch}</span>: {m.reason}</li>)}</ul>
            </details>
          ) : null}
          <div class="row">
            <button type="button" class="btn danger" disabled={removable.length === 0} onClick={() => setConfirming(true)}><Icon name="trash" /> {c.remove(removable.length)}</button>
            <CopyCommand command={theoCmd("worktree", "prune", chosen, "--merged")} />
          </div>
        </>
      ) : null}
      {confirming && res.data ? (
        <TypedConfirmDialog
          title={c.confirmTitle(removable.length, chosen)}
          lead={c.confirmLead}
          expected={chosen}
          affectedTitle={c.willRemove}
          affected={removable.map((m) => ({ name: m.branch, detail: m.path }))}
          preserved={[c.keepBranches, c.keepVolumes, c.keepClones]}
          confirmLabel={c.remove(removable.length)}
          onClose={() => setConfirming(false)}
          onConfirm={async (text) => {
            const out = await api.post<PruneReport>(`${projectPath(chosen)}/prune`, { confirm: text });
            announce(c.done(out.merged.filter((m) => m.removed).length));
            res.reload();
            void refreshStatus();
          }}
        />
      ) : null}
    </section>
  );
}
