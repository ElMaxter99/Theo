// V-16 Tools (0.9): the first tool, before/after screenshots. The page does what `theo tool capture run-pair` does and
// uses the API's operations, so there is no second copy of the logic: it starts the comparison, shows the same progress
// numbers the command line prints, and lists the reports and saved sets the command line made.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, ApiError, BASE, projectPath } from "../api/client.ts";
import type { Branch, CaptureReport, CaptureRun, CaptureState, Operation, Project } from "../api/types.ts";
import { Modal } from "../components/Dialog.tsx";
import { CopyCommand, theoCmd } from "../components/CopyCommand.tsx";
import { Icon } from "../components/Icon.tsx";
import { Banner, ErrorBanner, Field, Loading, PageHead } from "../components/ui.tsx";
import { useResource } from "../lib/hooks.ts";
import { announce } from "../lib/live.ts";
import { lang } from "../lib/locale.ts";
import { href, navigate } from "../lib/router.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

/** The comparison being followed. It lives outside the page so leaving and coming back keeps showing it. */
interface Followed { project: string; opId: string; a: string; b: string }
let followed: Followed | null = null;

const reportUrl = (project: string, name: string, file: string) =>
  `${BASE}${projectPath(project)}/capture/reports/${encodeURIComponent(name)}/${file}`;

export function Tools({ project }: { project?: string }) {
  useEffect(() => setTitle([t.tools.title]), []);
  const projects = useResource<Project[]>(() => api.get<Project[]>("/projects"), []);
  const list = projects.data ?? [];
  const current = project && list.some((p) => p.name === project) ? project : list[0]?.name;
  return (
    <>
      <PageHead title={t.tools.title} sub={t.tools.lead} />
      {projects.loading && !projects.data ? <Loading /> : null}
      <ErrorBanner error={projects.error} />
      {projects.data && list.length === 0 ? <Banner tone="info"><p>{t.tools.noProjects}</p></Banner> : null}
      {current ? <Capture project={current} projects={list.map((p) => p.name)} /> : null}
    </>
  );
}

function Capture({ project, projects }: { project: string; projects: string[] }) {
  const st = useResource<CaptureState>(() => api.get<CaptureState>(`${projectPath(project)}/capture`), [project], { project });
  const [run, setRun] = useState<Followed | null>(followed && followed.project === project ? followed : null);
  const [installing, setInstalling] = useState<Operation | null>(null);
  const [askInstall, setAskInstall] = useState(false);
  const s = st.data;

  const follow = (f: Followed | null) => { followed = f; setRun(f); };
  const finished = () => { follow(null); st.reload(); };

  // Installing the engine is an operation of its own; the state is read again when it ends.
  useEffect(() => {
    if (!installing) return;
    let stop = false;
    const tick = async () => {
      try {
        const op = await api.get<Operation>(`/operations/${encodeURIComponent(installing.id)}`);
        if (stop) return;
        if (op.state === "running" || op.state === "queued") { timer = setTimeout(tick, 800); return; }
        setInstalling(null);
        announce(op.state === "succeeded" ? t.tools.installed : t.tools.installFailed);
        st.reload();
      } catch { if (!stop) { setInstalling(null); st.reload(); } }
    };
    let timer = setTimeout(tick, 800);
    return () => { stop = true; clearTimeout(timer); };
  }, [installing?.id]);

  return (
    <section class="stack" aria-labelledby="tl-capture">
      <div class="row between">
        <h2 id="tl-capture">{t.tools.capture}</h2>
        {projects.length > 1 ? (
          <div class="row">
            <label for="tl-project">{t.tools.project}</label>
            <select id="tl-project" class="input auto" value={project} onChange={(e) => navigate(href.tools((e.currentTarget as HTMLSelectElement).value))}>
              {projects.map((p) => <option key={p} value={p}>{p}</option>)}
            </select>
          </div>
        ) : null}
      </div>
      {st.loading && !s ? <Loading /> : null}
      <ErrorBanner error={st.error} title={t.tools.loadError} />
      {s ? (
        <>
          {!s.engine ? (
            <Banner tone="warn" title={t.tools.engineMissingTitle} actions={
              <button type="button" class="btn primary" disabled={!!installing} onClick={() => setAskInstall(true)} data-testid="install-engine"><Icon name="download" /> {t.tools.install}</button>
            }>
              <p>{t.tools.engineMissingBody}</p>
              {s.problem ? <p class="small mono">{s.problem}</p> : null}
              {installing ? <p role="status"><Icon name="restart" spin /> {t.tools.installing}</p> : null}
            </Banner>
          ) : <p class="muted small" data-testid="engine-ready"><Icon name="ok" /> {t.tools.engineReady(s.node ?? "", s.chromium ?? "")}</p>}

          {s.no_inventory ? (
            <Banner tone="info" title={t.tools.noInventoryTitle}><p>{t.tools.noInventoryBody}</p></Banner>
          ) : s.inventory_error ? (
            <Banner tone="error" title={t.tools.inventoryBroken}><pre class="mono small prewrap">{s.inventory_error}</pre></Banner>
          ) : s.inventory ? (
            <p class="row" data-testid="inventory-summary">
              <span>{t.tools.inventorySummary(s.inventory.Viewports, s.inventory.Screens, s.inventory.States, s.inventory.Flows)}</span>
              <CopyCommand command={theoCmd("tool", "capture", "validate", project)} label={t.tools.validate} />
            </p>
          ) : null}

          {run ? <Running run={run} onDone={finished} /> : null}
          {s.engine && s.inventory && !run ? <CompareForm project={project} onStarted={(f) => follow(f)} /> : null}

          <Reports project={project} reports={s.reports} />
          <Sets sets={s.sets} />
        </>
      ) : null}
      {askInstall ? (
        <Modal title={t.tools.installTitle} onClose={() => setAskInstall(false)}>
          <p>{t.tools.installBody}</p>
          <div class="dialog-actions">
            <button type="button" class="btn" autoFocus onClick={() => setAskInstall(false)}>{t.app.cancel}</button>
            <button type="button" class="btn primary" onClick={async () => {
              setAskInstall(false);
              try { setInstalling(await api.post<Operation>("/capture/setup", {}, { idempotent: true })); } catch { /* shown by the next read */ }
            }}>{t.tools.installConfirm}</button>
          </div>
        </Modal>
      ) : null}
    </section>
  );
}

function CompareForm({ project, onStarted }: { project: string; onStarted: (f: Followed) => void }) {
  const branches = useResource<Branch[]>(() => api.get<Branch[]>(`${projectPath(project)}/branches`), [project]);
  // The branches that have a worktree: that is what Theo starts and captures.
  const names = (branches.data ?? []).filter((b) => !b.remote && b.worktree).map((b) => b.name);
  const [a, setA] = useState("");
  const [b, setB] = useState("");
  const [mode, setMode] = useState("");
  const [again, setAgain] = useState(false);
  const [err, setErr] = useState("");
  const [failure, setFailure] = useState<ApiError | undefined>();
  const [commands, setCommands] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const aRef = useRef<HTMLSelectElement>(null);

  useEffect(() => {
    if (names.length === 0) return;
    setA((x) => (names.includes(x) ? x : names[0]!));
    setB((x) => (names.includes(x) ? x : names[1] ?? names[0]!));
  }, [names.join("\u0000")]);

  const start = async (approve: boolean) => {
    if (!a || !b || a === b) { setErr(t.tools.pickTwo); aRef.current?.focus(); return; }
    setErr("");
    setFailure(undefined);
    setBusy(true);
    try {
      const op = await api.post<Operation>(`${projectPath(project)}/capture/compare`, { a, b, mode, again, lang, approve }, { idempotent: true });
      setCommands(null);
      onStarted({ project, opId: op.id, a, b });
    } catch (e) {
      const ae = e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) });
      if (ae.code === "run_not_approved" && !approve) setCommands((ae.body.original ?? "").split("\n").filter(Boolean));
      else setFailure(ae);
    } finally { setBusy(false); }
  };

  const command = theoCmd("tool", "capture", "run-pair", project, a || "<before>", b || "<after>", mode && `--mode`, mode, again && "--again");
  return (
    <form class="card stack" onSubmit={(e) => { e.preventDefault(); void start(false); }} aria-labelledby="tl-compare" data-testid="compare-form">
      <h3 id="tl-compare">{t.tools.compareTitle}</h3>
      <div class="row wrap">
        <Field id="tl-a" label={t.tools.before} error={err}>
          <select id="tl-a" ref={aRef} class="input mono" value={a} onChange={(e) => setA((e.currentTarget as HTMLSelectElement).value)}>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </Field>
        <Field id="tl-b" label={t.tools.after}>
          <select id="tl-b" class="input mono" value={b} onChange={(e) => setB((e.currentTarget as HTMLSelectElement).value)}>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </Field>
        <Field id="tl-mode" label={t.tools.data}>
          <select id="tl-mode" class="input" value={mode} onChange={(e) => setMode((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">{t.tools.modeDefault}</option>
            <option value="mock">{t.tools.modeMock}</option>
            <option value="real">{t.tools.modeReal}</option>
            <option value="grabar">{t.tools.modeGrabar}</option>
          </select>
        </Field>
      </div>
      <label class="row"><input type="checkbox" checked={again} onChange={(e) => setAgain((e.currentTarget as HTMLInputElement).checked)} /> {t.tools.again}</label>
      {branches.data && names.length < 2 ? <Banner tone="info"><p>{t.tools.needTwo}</p></Banner> : null}
      <ErrorBanner error={failure} />
      <div class="row">
        <button type="submit" class="btn primary" disabled={busy || names.length === 0} data-testid="start-compare"><Icon name="play" /> {t.tools.start}</button>
        <CopyCommand command={command} />
      </div>
      <p class="hint">{t.tools.startHint}</p>
      {commands ? (
        <Modal title={t.tools.approveTitle} onClose={() => setCommands(null)}>
          <p>{t.tools.approveBody}</p>
          <pre class="mono small prewrap" data-testid="approve-commands">{commands.join("\n")}</pre>
          <div class="dialog-actions">
            <button type="button" class="btn" autoFocus onClick={() => setCommands(null)}>{t.app.cancel}</button>
            <button type="button" class="btn primary" disabled={busy} onClick={() => void start(true)}>{t.tools.approveConfirm}</button>
          </div>
        </Modal>
      ) : null}
    </form>
  );
}

function Running({ run, onDone }: { run: Followed; onDone: () => void }) {
  const [view, setView] = useState<CaptureRun | null>(null);
  const [error, setError] = useState<ApiError | undefined>();
  const doneRef = useRef(onDone);
  doneRef.current = onDone;
  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const v = await api.get<CaptureRun>(`${projectPath(run.project)}/capture/runs/${encodeURIComponent(run.opId)}`);
        if (stop) return;
        setView(v);
        if (v.operation.state === "running" || v.operation.state === "queued" || v.operation.state === "cancelling") { timer = setTimeout(tick, 600); return; }
      } catch (e) {
        if (stop) return;
        setError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
      }
    };
    void tick();
    return () => { stop = true; clearTimeout(timer); };
  }, [run.opId]);

  const op = view?.operation;
  const active = !op || op.state === "running" || op.state === "queued" || op.state === "cancelling";
  const total = (view?.progress ?? []).reduce((n, p) => n + p.total, 0);
  const done = (view?.progress ?? []).reduce((n, p) => n + p.done, 0);
  useEffect(() => { if (op && !active) announce(op.state === "succeeded" ? t.tools.finished(op.result?.summary ?? "") : t.tools.failed); }, [active, op?.state]);
  const errText = op?.error ? (typeof op.error === "string" ? op.error : op.error.message ?? "") : "";
  return (
    <section class="card stack" aria-labelledby="tl-running" data-testid="capture-running" aria-live="polite">
      <h3 id="tl-running">{t.tools.running(run.a, run.b)}</h3>
      {active ? <div class="progress-line" aria-hidden="true" /> : null}
      <ul class="plain" data-testid="capture-progress">
        {(view?.progress ?? []).map((p) => <li key={p.label}>{t.tools.capturing(p.label, p.done, p.total)}</li>)}
      </ul>
      {active && total > 0 && done >= total ? <p class="muted">{t.tools.comparing}</p> : null}
      <ErrorBanner error={error} />
      {op && !active ? (
        op.state === "succeeded"
          ? <Banner tone="ok"><p>{t.tools.finished(op.result?.summary ?? "")}</p></Banner>
          : <Banner tone="error" title={t.tools.failed} role="alert">{errText ? <p class="mono small prewrap">{errText}</p> : null}</Banner>
      ) : null}
      {op && !active ? <div><button type="button" class="btn" onClick={() => doneRef.current()} data-testid="capture-close">{t.app.close}</button></div> : null}
    </section>
  );
}

function Reports({ project, reports }: { project: string; reports: CaptureReport[] }) {
  const k = t.tools;
  return (
    <section class="stack" aria-labelledby="tl-reports">
      <h3 id="tl-reports">{k.reportsTitle}</h3>
      {reports.length === 0 ? <p class="muted" data-testid="no-reports">{k.noReports}</p> : (
        <div class="table-wrap" tabIndex={0} role="region" aria-labelledby="tl-reports">
          <table class="data" data-testid="reports-table">
            <thead>
              <tr><th scope="col">{k.colReport}</th><th scope="col">{k.colBranches}</th><th scope="col">{k.colChanged}</th><th scope="col">{k.colNewRemoved}</th><th scope="col">{k.colNotCaptured}</th><th scope="col"><span class="sr-only">{k.reportsTitle}</span></th></tr>
            </thead>
            <tbody>
              {reports.map((r) => (
                <tr key={r.name} data-testid="report-row">
                  <th scope="row">{r.date}</th>
                  <td class="mono">{r.a && r.b ? `${r.a} → ${r.b}` : r.name}</td>
                  <td>{r.counts?.Changed ?? "–"}</td>
                  <td>{r.counts ? `${r.counts.New} / ${r.counts.Removed}` : "–"}</td>
                  <td>{r.counts?.NotCaptured ?? "–"}</td>
                  <td class="row">
                    {r.gallery ? <a class="btn sm" href={reportUrl(project, r.name, "composiciones.html")} target="_blank" rel="noopener">{k.compositions}<span class="sr-only"> ({k.openNewTab})</span></a> : null}
                    {r.corporate ? <a class="btn sm" href={reportUrl(project, r.name, "corporativo.html")} target="_blank" rel="noopener">{k.corporate}<span class="sr-only"> ({k.openNewTab})</span></a> : null}
                    {r.summary ? <a class="btn sm" href={reportUrl(project, r.name, "resumen.md")} target="_blank" rel="noopener">{k.summary}<span class="sr-only"> ({k.openNewTab})</span></a> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

function Sets({ sets }: { sets: CaptureState["sets"] }) {
  return (
    <section class="stack" aria-labelledby="tl-sets">
      <h3 id="tl-sets">{t.tools.setsTitle}</h3>
      {sets.length === 0 ? <p class="muted">{t.tools.noSets}</p> : (
        <ul class="plain" data-testid="sets-list">
          {sets.map((x) => <li key={`${x.label}|${x.commit}`} class="mono small">{t.tools.setLine(x.label, x.commit ?? "", x.shots, x.unstable)}</li>)}
        </ul>
      )}
    </section>
  );
}
