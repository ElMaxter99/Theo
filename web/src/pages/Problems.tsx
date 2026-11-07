// V-08 Problems and cleanup. "Problems": the warnings of the instances (ports, env files, Compose files that changed,
// forgotten worktrees) with what to do, and the differences between Theo's records and Git and Docker. "Cleanup": the
// forgotten work of ADR-0016 (below).
import { useEffect, useState } from "preact/hooks";
import { api, ApiError, projectPath, qs } from "../api/client.ts";
import type { CleanEntry, CleanReport, LifeReport, LifeWorktree, ParkInactiveReport } from "../api/types.ts";
import { KeepDialog, ParkDialog } from "../components/LifecycleDialogs.tsx";
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

// V-08 Cleanup (0.4 board "Limpieza"): what can be closed (merged, also by squash, or folder gone) with the options
// to take instances down, delete branches and images; the inactive ones with Park and Keep; what stays and why; and the
// Docker leftovers of worktrees already removed. Closing needs the word "all" typed, and sends each project's name.
function Cleanup({ initial }: { initial?: string }) {
  const status = useStatus();
  const names = (status.report?.projects ?? []).map((p) => p.name);
  const [project, setProject] = useState(initial ?? "");
  const chosen = project && names.includes(project) ? [project] : names;
  const key = chosen.join("|");
  const c = t.cleanup;
  const plans = useResource<Plan[]>(async () => Promise.all(chosen.map(async (p) => {
    const [clean, life] = await Promise.all([
      api.get<CleanReport>(`${projectPath(p)}/clean`),
      api.get<LifeReport>(`${projectPath(p)}/lifecycle${qs({ size: true })}`).catch(() => null),
    ]);
    return { project: p, clean, life };
  })), [key], { project: chosen.length === 1 ? chosen[0] : "*" });
  const [down, setDown] = useState(false);
  const [branches, setBranches] = useState(false);
  const [images, setImages] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [parking, setParking] = useState<{ project: string; w: LifeWorktree } | null>(null);
  const [keeping, setKeeping] = useState<{ project: string; w: LifeWorktree } | null>(null);
  const [parkingAll, setParkingAll] = useState(false);
  const [parkAll, setParkAll] = useState(false);
  const [saveChanges, setSaveChanges] = useState(false);
  const [withIgnored, setWithIgnored] = useState(false);
  const [parkPlan, setParkPlan] = useState<ParkInactiveReport | null>(null);
  const [actionError, setActionError] = useState<ApiError | undefined>();
  const data = plans.data ?? [];
  const lifeOf = (p: Plan, path: string) => p.life?.worktrees.find((w) => w.path === path);
  const closable = data.flatMap((p) => p.clean.close.filter((e) => !e.reason).map((e) => ({ p, e, w: lifeOf(p, e.path) })));
  const staying = data.flatMap((p) => [...p.clean.close.filter((e) => e.reason), ...p.clean.kept].map((e) => ({ p, e })));
  const inactive = data.flatMap((p) => p.clean.inactive.map((e) => ({ p, e, w: lifeOf(p, e.path) })));
  const leftovers = data.flatMap((p) => p.clean.leftovers.map((f) => ({ p, f })));
  const projectsWith = [...new Set(closable.map((x) => x.p.project))];
  const reload = () => { plans.reload(); void refreshStatus(); };
  // What "park them all" would do with the options chosen: a listing, nothing changes.
  const inactiveProjects = [...new Set(inactive.map((x) => x.p.project))];
  useEffect(() => {
    if (!parkAll || inactiveProjects.length !== 1) { setParkPlan(null); return; }
    let live = true;
    api.get<ParkInactiveReport>(`${projectPath(inactiveProjects[0]!)}/park-inactive${qs({ save_changes: saveChanges, include_ignored: withIgnored })}`)
      .then((r) => { if (live) setParkPlan(r); }, () => { if (live) setParkPlan(null); });
    return () => { live = false; };
  }, [parkAll, saveChanges, withIgnored, inactiveProjects.join("|"), plans.data]);
  const parkable = parkPlan?.entries.filter((x) => !x.reason) ?? [];
  const parkStays = parkPlan?.entries.filter((x) => x.reason) ?? [];
  const cmd = chosen.length === 1
    ? theoCmd("worktree", "clean", chosen[0], down && "--down", branches && "--delete-branches", images && "--images", "--confirm", chosen[0])
    : theoCmd("worktree", "clean", "--all", down && "--down", branches && "--delete-branches", images && "--images", "--confirm", "all");
  const why = (e: CleanEntry) => e.state === "missing" ? c.folderGone : e.merged_by === "content" ? c.mergedSquash : c.mergedInto(lifeBase(data, e) || "main");

  return (
    <section class="stack" aria-labelledby="cl-title">
      <h2 id="cl-title">{c.title}</h2>
      <p>{c.lead}</p>
      <div class="row controls">
        <div class="field">
          <label for="cl-project">{c.project}</label>
          <select id="cl-project" class="input auto" value={project} onChange={(e) => setProject((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">{c.all}</option>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </div>
        <button type="button" class="btn sm" onClick={reload}>{c.check}</button>
      </div>
      {plans.loading && !plans.data ? <Loading /> : null}
      <ErrorBanner error={plans.error ?? actionError} />
      {plans.data ? (
        <>
          <h3>{c.closable(closable.length)} <span class="small muted">{c.closableHint}</span></h3>
          {closable.length === 0 ? <Banner tone="ok"><p>{c.nothing}</p></Banner> : (
            <div class="table-wrap" tabIndex={0} role="region" aria-label={c.closable(closable.length)}>
              <table class="data">
                <caption class="sr-only">{c.closable(closable.length)}</caption>
                <thead><tr><th scope="col">{t.panel.colWorktree}</th><th scope="col">{c.colWhy}</th><th scope="col">{t.life.colInstance}</th><th scope="col" class="num">{c.colSize}</th></tr></thead>
                <tbody>{closable.map(({ p, e, w }) => (
                  <tr key={e.path}>
                    <th scope="row">{e.branch ?? e.path} <span class="small muted">{p.project}</span></th>
                    <td>{why(e)}{w?.upstream_gone ? <span class="small muted"> · {c.upstreamGone}</span> : null}</td>
                    <td>{w?.running ? <>{t.life.state["in-use"]} <span class="small muted">{c.willGoDown}</span></> : <span class="muted">—</span>}</td>
                    <td class="num">{sizeText(w?.size_bytes)}</td>
                  </tr>
                ))}</tbody>
              </table>
            </div>
          )}
          <fieldset class="stack-sm">
            <label class="row"><input type="checkbox" checked={down} onChange={(e) => setDown((e.currentTarget as HTMLInputElement).checked)} /> {c.optDown}</label>
            <label class="row"><input type="checkbox" checked={branches} onChange={(e) => setBranches((e.currentTarget as HTMLInputElement).checked)} /> {c.optBranches}</label>
            <label class="row"><input type="checkbox" checked={images} onChange={(e) => setImages((e.currentTarget as HTMLInputElement).checked)} /> {c.optImages}</label>
          </fieldset>
          <div class="row">
            <button type="button" class="btn danger" disabled={closable.length === 0} onClick={() => setConfirming(true)}><Icon name="trash" /> {c.closeN(closable.length)}</button>
            <CopyCommand command={cmd} />
          </div>

          <h3>{c.inactiveTitle(inactive.length)} <span class="small muted">{c.inactiveHint}</span></h3>
          {chosen.length === 1 ? <Threshold project={chosen[0]!} days={data[0]?.clean.inactive_after_days ?? 7} onSaved={reload} /> : null}
          {inactive.length > 0 ? (
            <ul class="list-rows">
              {inactive.map(({ p, e, w }) => (
                <li key={e.path} class="list-row">
                  <span class="grow"><strong>{e.branch ?? e.path}</strong> <span class="small muted">{c.inactiveRow(e.idle_days, p.project)}{w?.running ? ` · ${c.idleInstance}` : w && w.changes > 0 ? ` · ${c.changes(w.changes)}` : ""}{e.upstream_gone ? ` · ${c.remoteGone}` : ""}</span></span>
                  {w ? <button type="button" class="btn sm" disabled={w.changes > 0} onClick={() => setParking({ project: p.project, w })}>{t.life.park}</button> : null}
                  {w ? <button type="button" class="btn sm" onClick={() => setKeeping({ project: p.project, w })}>{t.life.keep}</button> : null}
                </li>
              ))}
            </ul>
          ) : null}
          <p class="small muted">{c.parkNote}</p>
          {inactive.length > 1 && inactiveProjects.length === 1 ? (
            <div class="stack-sm">
              <button type="button" class="btn sm" aria-expanded={parkAll} onClick={() => setParkAll(!parkAll)}>{c.parkAllTitle}</button>
              {parkAll ? (
                <>
                  <fieldset class="stack-sm">
                    <label class="row"><input type="checkbox" checked={saveChanges} onChange={(e) => setSaveChanges((e.currentTarget as HTMLInputElement).checked)} /> {c.parkAllSave}</label>
                    <label class="row"><input type="checkbox" checked={withIgnored} onChange={(e) => setWithIgnored((e.currentTarget as HTMLInputElement).checked)} /> {c.parkAllIgnored}</label>
                  </fieldset>
                  {parkPlan ? (
                    <>
                      <p>{c.parkAllPlan(parkable.length)}</p>
                      {parkStays.length > 0 ? (
                        <>
                          <p class="small">{c.parkAllStays}</p>
                          <ul class="small">{parkStays.map((x) => <li key={x.path}><span class="mono">{x.branch ?? x.path}</span> · {x.reason}</li>)}</ul>
                        </>
                      ) : null}
                    </>
                  ) : <Loading />}
                  <div class="row">
                    <button type="button" class="btn danger" disabled={parkable.length === 0} onClick={() => setParkingAll(true)}>{c.parkAllN(parkable.length)}</button>
                    <CopyCommand command={theoCmd("worktree", "park-inactive", inactiveProjects[0]!, saveChanges && "--save-changes", withIgnored && "--include-ignored", "--confirm", inactiveProjects[0]!)} />
                  </div>
                </>
              ) : null}
            </div>
          ) : null}

          {staying.length > 0 ? (
            <>
              <h3>{c.stayingTitle(staying.length)}</h3>
              <ul>{staying.map(({ p, e }) => <li key={e.path}><span class="mono">{e.branch ?? e.path}</span> · {p.project} · {e.reason ?? (e.note ? `${t.life.state.kept} · «${e.note}»` : t.life.state.kept)}</li>)}</ul>
            </>
          ) : null}

          {leftovers.length > 0 ? (
            <>
              <h3>{c.leftoversTitle(leftovers.length)}</h3>
              <p class="small">{c.leftoversHint}</p>
              <ul>{leftovers.map(({ p, f }) => <li key={f.id}><span class="mono">{f.subject}</span> · {p.project} · {f.kind}{f.fix && !f.forgettable ? <> · <code>{f.fix}</code></> : null}</li>)}</ul>
              {leftovers.some(({ f }) => f.forgettable) ? (
                <button type="button" class="btn sm" onClick={() => void api.post("/reconcile/apply", { ids: leftovers.filter(({ f }) => f.forgettable).map(({ f }) => f.id) }).then(reload, (e) => setActionError(e instanceof ApiError ? e : undefined))}>{c.removeLeftovers}</button>
              ) : null}
            </>
          ) : null}
        </>
      ) : null}
      {confirming ? (
        <TypedConfirmDialog
          title={c.closeN(closable.length)}
          lead={c.typeWord(c.word, closable.length)}
          expected={c.word}
          affectedTitle={c.closable(closable.length)}
          affected={closable.map(({ p, e }) => ({ name: e.branch ?? e.path, detail: `${p.project} · ${why(e)}` }))}
          preserved={[c.keepBranches, c.keepVolumes]}
          confirmLabel={c.closeN(closable.length)}
          onClose={() => setConfirming(false)}
          onConfirm={async () => {
            let n = 0;
            for (const p of projectsWith) {
              const out = await api.post<CleanReport>(`${projectPath(p)}/clean`, { down, delete_branches: branches, images, confirm: p });
              n += out.close.filter((e) => e.closed).length;
            }
            announce(c.done(n));
            reload();
          }}
        />
      ) : null}
      {parkingAll && inactiveProjects.length === 1 ? (
        <TypedConfirmDialog
          title={c.parkAllN(parkable.length)}
          lead={c.parkAllTyped(c.word, parkable.length)}
          expected={c.word}
          affectedTitle={c.parkAllTitle}
          affected={parkable.map((x) => ({ name: x.branch ?? x.path, detail: `${c.inactiveRow(x.idle_days, inactiveProjects[0]!)}${x.will_save ? ` · ${c.changes(x.changes)}` : ""}` }))}
          preserved={[c.parkAllKeep]}
          confirmLabel={c.parkAllN(parkable.length)}
          onClose={() => setParkingAll(false)}
          onConfirm={async () => {
            const out = await api.post<ParkInactiveReport>(`${projectPath(inactiveProjects[0]!)}/park-inactive`, { save_changes: saveChanges, include_ignored: withIgnored, confirm: inactiveProjects[0] });
            announce(c.parkAllDone(out.entries.filter((x) => x.parked).length));
            reload();
          }}
        />
      ) : null}
      {parking ? <ParkDialog project={parking.project} w={parking.w} onClose={() => setParking(null)} onDone={reload} /> : null}
      {keeping ? <KeepDialog project={keeping.project} w={keeping.w} onClose={() => setKeeping(null)} onDone={reload} /> : null}
    </section>
  );
}

interface Plan { project: string; clean: CleanReport; life: LifeReport | null }

function lifeBase(data: Plan[], e: CleanEntry): string {
  for (const p of data) {
    const w = p.life?.worktrees.find((x) => x.path === e.path);
    if (w?.base) return w.base;
  }
  return "";
}

function Threshold({ project, days, onSaved }: { project: string; days: number; onSaved: () => void }) {
  const [value, setValue] = useState(String(days));
  const [error, setError] = useState<ApiError | undefined>();
  const save = async () => {
    const n = Number(value);
    if (!Number.isInteger(n) || n < 1 || n > 90 || n === days) { setValue(String(days)); return; }
    try {
      await api.patch(projectPath(project), { inactive_after_days: n });
      announce(t.cleanup.thresholdSaved(n));
      onSaved();
    } catch (e) {
      setError(e instanceof ApiError ? e : undefined);
    }
  };
  return (
    <div class="field">
      <label for="cl-days">{t.cleanup.threshold(project)}</label>
      <input id="cl-days" class="input auto" type="number" min={1} max={90} value={value} onInput={(e) => setValue((e.currentTarget as HTMLInputElement).value)} onBlur={() => void save()} onKeyDown={(e) => { if (e.key === "Enter") void save(); }} />
      <ErrorBanner error={error} />
    </div>
  );
}
