// "New branch + instance" wizard (view-phase 2.4): Branch -> Location and environment -> Ports -> Summary.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, ApiError, projectPath, qs, wtPath } from "../api/client.ts";
import type { AddedWorktree, Branch, Overview, PortPlan, Project, Worktree, WorktreeSettingsInfo } from "../api/types.ts";
import { Banner, Crumbs, ErrorBanner, Field, Loading, PageHead, describedBy } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { useResource } from "../lib/hooks.ts";
import { clashesForNewInstance, hasFixedPorts } from "../lib/ports.ts";
import { folderForBranch, validBranchName } from "../lib/format.ts";
import { announce } from "../lib/live.ts";
import { href, navigate } from "../lib/router.ts";
import { startOperation } from "../ops/store.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

type PortChoice = "auto" | "ignore";

export function Wizard({ project }: { project: string }) {
  const [step, setStep] = useState(1);
  const [mode, setMode] = useState<"existing" | "new">("new");
  const [branch, setBranch] = useState("");
  const [newName, setNewName] = useState("");
  const [from, setFrom] = useState("");
  const [folder, setFolder] = useState("");
  const [portChoice, setPortChoice] = useState<PortChoice>("auto");
  const [startNow, setStartNow] = useState(true);
  // Explicit, never silent: off unless the user ticks it (and only offered when the project has fixed container names).
  const [neutralize, setNeutralize] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | undefined>();
  const [created, setCreated] = useState<{ added: AddedWorktree; id?: string; startFailed?: string } | null>(null);
  const heading = useRef<HTMLHeadingElement>(null);

  const branches = useResource<Branch[]>(() => api.get<Branch[]>(`${projectPath(project)}/branches`), [project]);
  const overview = useResource<Overview>(() => api.get<Overview>(`${projectPath(project)}/overview`), [project]);
  const projects = useResource<Project[]>(() => api.get<Project[]>("/projects"), []);
  const projInfo = projects.data?.find((p) => p.name === project);
  const mainWt: Worktree | undefined = overview.data?.worktrees.find((w) => w.main) ?? overview.data?.worktrees[0];
  const plan = useResource<PortPlan | null>(
    (signal) => (step === 3 && mainWt ? api.get<PortPlan>(`${wtPath(project, mainWt.id)}/ports${qs({ auto_ports: true })}`, { signal }) : Promise.resolve(null)),
    [step === 3, mainWt?.id],
  );

  const settings = useResource<WorktreeSettingsInfo | null>(
    (signal) => (mainWt ? api.get<WorktreeSettingsInfo>(`${wtPath(project, mainWt.id)}/settings`, { signal }) : Promise.resolve(null)),
    [mainWt?.id],
  );
  const fixedNames = Object.entries(settings.data?.fixed_container_names ?? {});

  useEffect(() => setTitle([t.wizard.title, project]), [project]);
  useEffect(() => { heading.current?.focus(); }, [step, created]);
  useEffect(() => { if (!from && mainWt?.branch) setFrom(mainWt.branch); }, [mainWt?.branch]);

  const list = branches.data ?? [];
  const chosen = mode === "new" ? newName.trim() : branch;
  const clashes = clashesForNewInstance(plan.data ?? undefined);
  const proposal = projInfo?.worktree_dir_effective ? `${projInfo.worktree_dir_effective}/${folderForBranch(chosen || "branch")}` : "";

  // The one polite live region tells screen-reader users the outcome of the port check.
  useEffect(() => {
    if (step !== 3 || plan.loading) return;
    announce(plan.error ? t.wizard.previewError : clashes.length > 0 ? t.wizard.s3Conflict(clashes.length) : hasFixedPorts(plan.data ?? undefined) ? t.wizard.s3NoConflict : t.wizard.s3NoPorts);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, plan.loading, plan.error, clashes.length]);

  const validate1 = (): string => {
    if (mode === "existing") {
      if (!branch) return t.wizard.branchRequired;
      const b = list.find((x) => x.name === branch);
      if (b?.worktree) return t.wizard.inUse(b.worktree);
      return "";
    }
    if (!newName.trim()) return t.wizard.branchRequired;
    if (!validBranchName(newName)) return t.wizard.nameInvalid;
    if (list.some((b) => b.name === newName.trim())) return t.wizard.nameTaken;
    return "";
  };

  const next = () => {
    if (step === 1) {
      const e = validate1();
      setErr(e);
      if (e) { document.getElementById(mode === "new" ? "wz-new" : "wz-branches")?.focus(); return; }
    }
    setErr("");
    setStep(step + 1);
  };

  const create = async () => {
    setBusy(true);
    setFailure(undefined);
    let added: AddedWorktree;
    try {
      const body: Record<string, unknown> = { branch: chosen };
      if (mode === "new") { body.new = true; if (from) body.from = from; }
      if (folder.trim()) body.path = folder.trim();
      added = await api.post<AddedWorktree>(`${projectPath(project)}/worktrees`, body);
    } catch (e) {
      setFailure(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
      setBusy(false);
      return;
    }
    announce(t.wizard.created(added.branch));
    let id = added.id;
    if (!id) {
      try {
        const ov = await api.get<Overview>(`${projectPath(project)}/overview`);
        id = ov.worktrees.find((w) => w.path === added.path)?.id;
      } catch { /* the detail link falls back to the project panel */ }
    }
    let startFailed: string | undefined;
    if (neutralize && id) {
      try { await api.patch(`${wtPath(project, id)}/settings`, { neutralize_container_names: true }); }
      catch (e) { startFailed = t.wizard.neutralizeFailed(e instanceof ApiError ? e.message : String(e)); }
    }
    if (startNow && id && !startFailed) {
      try { await startOperation(project, id, "up", { auto_ports: portChoice === "auto" }); }
      catch (e) { startFailed = e instanceof ApiError ? e.message : String(e); }
    }
    setBusy(false);
    const notes = added.notes ?? [];
    if (notes.length === 0 && !startFailed && id) { navigate(href.detail(project, id, startNow ? "logs" : "summary")); return; }
    setCreated({ added, id, startFailed });
  };

  if (created) {
    return (
      <>
        <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project, href: href.project(project) }, { label: t.wizard.title }]} />
        <PageHead title={t.wizard.created(created.added.branch)} headingRef={heading} />
        {created.startFailed ? <Banner tone="error" title={t.wizard.startError} role="alert"><p>{created.startFailed}</p><p>{t.detail.keptNote}</p></Banner> : null}
        {(created.added.notes ?? []).length > 0 ? (
          <Banner tone="info" title={t.wizard.notesTitle}><ul>{(created.added.notes ?? []).map((n, i) => <li key={i}>{n}</li>)}</ul></Banner>
        ) : null}
        <p class="mono small">{created.added.path}</p>
        <p><a class="btn primary" href={created.id ? href.detail(project, created.id, startNow ? "logs" : "summary") : href.project(project)}>{t.wizard.gotoDetail}</a></p>
      </>
    );
  }

  const stepTitles = [t.wizard.s1Title, t.wizard.s2Title, t.wizard.s3Title, t.wizard.s4Title];
  const endsWithStart = startNow;

  return (
    <>
      <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project, href: href.project(project) }, { label: t.wizard.title }]} />
      <PageHead title={t.wizard.title} />
      <ol class="stepper" aria-label={t.wizard.stepsLabel}>
        {t.wizard.steps.map((s, i) => <li key={s} aria-current={step === i + 1 ? "step" : undefined}>{s}</li>)}
      </ol>
      <h2 class="wizard-heading" ref={heading} tabIndex={-1}>{t.wizard.stepOf(step, 4)}: {stepTitles[step - 1]}</h2>

      {step === 1 ? (
        <div>
          <div class="radio-card"><input id="wz-mode-new" type="radio" name="mode" checked={mode === "new"} onChange={() => { setMode("new"); setErr(""); }} /><label for="wz-mode-new">{t.wizard.brandNew}</label></div>
          <div class="radio-card"><input id="wz-mode-ex" type="radio" name="mode" checked={mode === "existing"} onChange={() => { setMode("existing"); setErr(""); }} /><label for="wz-mode-ex">{t.wizard.existing}</label></div>
          {mode === "new" ? (
            <>
              <Field id="wz-new" label={t.wizard.newName} hint={t.wizard.newNameHint} error={err}>
                <input id="wz-new" class="input mono" value={newName} autocomplete="off" spellcheck={false} onInput={(e) => setNewName((e.currentTarget as HTMLInputElement).value)} {...describedBy("wz-new", t.wizard.newNameHint, err)} />
              </Field>
              <Field id="wz-from" label={t.wizard.from} hint={t.wizard.fromHint}>
                <select id="wz-from" class="input" value={from} onChange={(e) => setFrom((e.currentTarget as HTMLSelectElement).value)} {...describedBy("wz-from", t.wizard.fromHint)}>
                  {list.filter((b) => !b.remote).map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
                </select>
              </Field>
            </>
          ) : (
            <fieldset id="wz-branches" tabIndex={-1} aria-describedby="wz-branch-hint">
              <legend>{t.wizard.branch}</legend>
              <p class="hint" id="wz-branch-hint">{t.wizard.branchHint}</p>
              {branches.loading && !branches.data ? <Loading /> : null}
              <ErrorBanner error={branches.error} />
              {list.map((b, i) => (
                <div class="radio-card" key={b.name + b.remote}>
                  <input id={`wz-b-${i}`} type="radio" name="branch" disabled={!!b.worktree} checked={branch === b.name} onChange={() => { setBranch(b.name); setErr(""); }} />
                  <label for={`wz-b-${i}`}>
                    <span class="mono">{b.name}</span>{b.remote ? t.wizard.remoteSuffix : ""}
                    {b.worktree ? <span class="muted small"> — {t.wizard.inUse(b.worktree)}</span> : null}
                  </label>
                </div>
              ))}
              {err ? <p class="field-error"><Icon name="error" /> <span>{err}</span></p> : null}
            </fieldset>
          )}
        </div>
      ) : null}

      {step === 2 ? (
        <div>
          <Field id="wz-folder" label={t.wizard.folder} hint={t.wizard.folderHint(proposal)}>
            <input id="wz-folder" class="input mono" value={folder} placeholder={proposal} autocomplete="off" spellcheck={false} onInput={(e) => setFolder((e.currentTarget as HTMLInputElement).value)} {...describedBy("wz-folder", t.wizard.folderHint(proposal))} />
          </Field>
          <h3>{t.wizard.envTitle}</h3>
          <p>{projInfo?.env_file ? t.wizard.envProject(projInfo.env_file) : t.wizard.envMain}</p>
          <p class="hint">{t.wizard.envNote}</p>
          {fixedNames.length > 0 ? (
            <div class="field">
              <h3>{t.wizard.fixedNamesTitle}</h3>
              <p id="wz-fixed-desc">{t.wizard.fixedNamesBody(fixedNames.map(([svc, n]) => `${svc}: ${n}`).join(", "))}</p>
              <div class="check">
                <input id="wz-neutralize" type="checkbox" checked={neutralize} aria-describedby="wz-fixed-desc wz-fixed-hint" onChange={(e) => setNeutralize((e.currentTarget as HTMLInputElement).checked)} />
                <label for="wz-neutralize">{t.wizard.neutralize}</label>
              </div>
              <p class="hint" id="wz-fixed-hint">{t.wizard.neutralizeHint}</p>
            </div>
          ) : null}
        </div>
      ) : null}

      {step === 3 ? (
        <div>
          {plan.loading || overview.loading ? <Loading label={t.wizard.s3Loading} /> : null}
          {plan.error ? <Banner tone="warn"><p>{t.wizard.previewError}</p></Banner> : null}
          {!plan.loading && !plan.error ? (
            clashes.length === 0 ? (
              <Banner tone="ok"><p>{hasFixedPorts(plan.data ?? undefined) ? t.wizard.s3NoConflict : t.wizard.s3NoPorts}</p></Banner>
            ) : (
              <>
                <Banner tone="warn"><p>{t.wizard.s3Conflict(clashes.length)}</p></Banner>
                <div class="table-wrap" tabIndex={0} role="region" aria-label={t.wizard.s3Title}>
                  <table class="data">
                    <caption class="sr-only">{t.wizard.s3Title}</caption>
                    <thead><tr><th scope="col">{t.wizard.colService}</th><th scope="col" class="num">{t.wizard.colWanted}</th><th scope="col">{t.wizard.colProblem}</th><th scope="col">{t.wizard.colAfter}</th></tr></thead>
                    <tbody>
                      {clashes.map((c, i) => (
                        <tr key={i}>
                          <th scope="row">{c.service}</th>
                          <td class="num mono">{c.port}</td>
                          <td><span class="badge error"><Icon name="portbusy" /> <span>{t.wizard.s3Explain(c.port, c.who === "instance" ? "another instance" : "another program")}</span></span></td>
                          <td>{portChoice === "auto" ? t.wizard.afterAuto : t.wizard.afterSame}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <fieldset>
                  <legend>{t.wizard.s3Title}</legend>
                  <div class="radio-card"><input id="wz-pa" type="radio" name="ports" checked={portChoice === "auto"} onChange={() => setPortChoice("auto")} /><label for="wz-pa"><strong>{t.wizard.optAuto}</strong><br /><span class="hint">{t.wizard.optAutoHint}</span></label></div>
                  <div class="radio-card"><input id="wz-pi" type="radio" name="ports" checked={portChoice === "ignore"} onChange={() => setPortChoice("ignore")} /><label for="wz-pi"><strong>{t.wizard.optIgnore}</strong><br /><span class="hint">{t.wizard.optIgnoreHint}</span></label></div>
                </fieldset>
              </>
            )
          ) : null}
        </div>
      ) : null}

      {step === 4 ? (
        <div>
          <p>{t.wizard.s4Lead}</p>
          <ul class="summary-list">
            <li>{t.wizard.doCreate(chosen, mode === "new", from || "HEAD")}</li>
            <li>{folder.trim() ? t.wizard.doFolder(folder.trim()) : t.wizard.doFolderAuto}</li>
            {startNow ? <li>{t.wizard.doPorts.replace("automatically", portChoice === "auto" ? "automatically" : "only when needed")}</li> : null}
            {neutralize && fixedNames.length > 0 ? <li>{t.wizard.doNeutralize}</li> : null}
            <li>{endsWithStart ? t.wizard.doStart : t.wizard.doNoStart}</li>
          </ul>
          <div class="check field">
            <input id="wz-start" type="checkbox" checked={startNow} onChange={(e) => setStartNow((e.currentTarget as HTMLInputElement).checked)} />
            <label for="wz-start">{t.wizard.startNow}</label>
          </div>
          <p class="hint">{t.wizard.keepNote}</p>
          <ErrorBanner error={failure} title={t.wizard.createError} />
        </div>
      ) : null}

      <div class="row">
        {step > 1 ? <button type="button" class="btn" onClick={() => setStep(step - 1)} disabled={busy}>{t.app.back}</button> : null}
        {step < 4 ? <button type="button" class="btn primary" onClick={next}>{t.app.next}</button> : null}
        {step === 4 ? <button type="button" class="btn primary" onClick={() => void create()} disabled={busy}>{busy ? t.wizard.creating : startNow ? t.wizard.createAndStart : t.wizard.create}</button> : null}
        <a class="btn quiet" href={href.project(project)}>{t.wizard.cancelWizard}</a>
      </div>
    </>
  );
}
