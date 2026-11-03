// V-04 New instance: one side panel instead of the four-step wizard of 0.1. The usual case needs only a branch name
// (a new branch from the main one); the port check runs while you type, and the rest is under "More options".
import { useEffect, useRef, useState } from "preact/hooks";
import { api, ApiError, projectPath, qs, wtPath } from "../api/client.ts";
import type { AddedWorktree, Branch, Overview, PortPlan, Project, WorktreeSettingsInfo } from "../api/types.ts";
import { Modal } from "../components/Dialog.tsx";
import { Banner, ErrorBanner, Field, Loading, describedBy } from "../components/ui.tsx";
import { CopyCommand, theoCmd } from "../components/CopyCommand.tsx";
import { useResource } from "../lib/hooks.ts";
import { clashesForNewInstance, hasFixedPorts } from "../lib/ports.ts";
import { folderForBranch, validBranchName } from "../lib/format.ts";
import { announce } from "../lib/live.ts";
import { href, navigate } from "../lib/router.ts";
import { startOperation } from "../ops/store.ts";
import { t } from "../strings.ts";

export function NewInstance({ project, overview, onClose }: { project: string; overview: Overview | undefined; onClose: () => void }) {
  const n = t.newInstance;
  const [mode, setMode] = useState<"new" | "existing">("new");
  const [name, setName] = useState("");
  const [branch, setBranch] = useState("");
  const [from, setFrom] = useState("");
  const [owner, setOwner] = useState("");
  const [folder, setFolder] = useState("");
  const [copy, setCopy] = useState(true);
  const [clone, setClone] = useState(false);
  const [neutralize, setNeutralize] = useState(false);
  const [autoPorts, setAutoPorts] = useState(true);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [failure, setFailure] = useState<ApiError | undefined>();
  const [notes, setNotes] = useState<{ added: AddedWorktree; id?: string; startFailed?: string } | null>(null);
  const nameRef = useRef<HTMLInputElement>(null);

  const branches = useResource<Branch[]>(() => api.get<Branch[]>(`${projectPath(project)}/branches`), [project]);
  const projects = useResource<Project[]>(() => api.get<Project[]>("/projects"), []);
  const info = projects.data?.find((p) => p.name === project);
  const main = overview?.worktrees.find((w) => w.main) ?? overview?.worktrees[0];
  const plan = useResource<PortPlan | null>(
    (signal) => (main ? api.get<PortPlan>(`${wtPath(project, main.id)}/ports${qs({ auto_ports: true })}`, { signal }) : Promise.resolve(null)), [main?.id]);
  const settings = useResource<WorktreeSettingsInfo | null>(
    (signal) => (main ? api.get<WorktreeSettingsInfo>(`${wtPath(project, main.id)}/settings`, { signal }) : Promise.resolve(null)), [main?.id]);
  const fixed = Object.entries(settings.data?.fixed_container_names ?? {});
  useEffect(() => { if (!from && main?.branch) setFrom(main.branch); }, [main?.branch]);

  const list = branches.data ?? [];
  const chosen = mode === "new" ? name.trim() : branch;
  const clashes = clashesForNewInstance(plan.data ?? undefined);
  const proposal = info?.worktree_dir_effective ? `${info.worktree_dir_effective}/${folderForBranch(chosen || n.branchPlaceholder)}` : "";
  const copyPatterns = info?.copy_on_add ?? [];

  const validate = (): string => {
    if (mode === "existing") {
      if (!branch) return n.branchRequired;
      const b = list.find((x) => x.name === branch);
      return b?.worktree ? t.wizard.inUse(b.worktree) : "";
    }
    if (!name.trim()) return n.branchRequired;
    if (!validBranchName(name)) return t.wizard.nameInvalid;
    if (list.some((b) => b.name === name.trim())) return t.wizard.nameTaken;
    return "";
  };

  const command = theoCmd("worktree", "add", project, chosen || n.branchPlaceholder, mode === "new" ? "--new" : "", mode === "new" && from ? "--from" : "", mode === "new" ? from : "",
    folder.trim() ? "--path" : "", folder.trim(), owner.trim() ? "--owner" : "", owner.trim(), copy && copyPatterns.length > 0 ? "--copy" : "", clone ? "--clone" : "");

  const create = async (start: boolean) => {
    const e = validate();
    setErr(e);
    if (e) { (mode === "new" ? nameRef.current : document.getElementById("ni-branch"))?.focus(); return; }
    setBusy(true);
    setFailure(undefined);
    let added: AddedWorktree;
    try {
      const body: Record<string, unknown> = { branch: chosen };
      if (mode === "new") { body.new = true; if (from) body.from = from; }
      if (folder.trim()) body.path = folder.trim();
      if (owner.trim()) body.owner = owner.trim();
      if (copy && copyPatterns.length > 0) body.copy = true;
      if (clone) body.clone = true;
      added = await api.post<AddedWorktree>(`${projectPath(project)}/worktrees`, body);
    } catch (err) {
      setFailure(err instanceof ApiError ? err : new ApiError(0, { code: "internal", message: String(err) }));
      setBusy(false);
      return;
    }
    announce(t.wizard.created(added.branch));
    const id = added.id;
    let startFailed: string | undefined;
    if (neutralize && id) {
      try { await api.patch(`${wtPath(project, id)}/settings`, { neutralize_container_names: true }); }
      catch (err) { startFailed = t.wizard.neutralizeFailed(err instanceof ApiError ? err.message : String(err)); }
    }
    if (start && id && !startFailed) {
      try { await startOperation(project, id, "up", { auto_ports: autoPorts }); }
      catch (err) { startFailed = err instanceof ApiError ? err.message : String(err); }
    }
    setBusy(false);
    if ((added.notes ?? []).length === 0 && !startFailed && id) { navigate(href.detail(project, id, start ? "logs" : "summary")); return; }
    setNotes({ added, id, startFailed });
  };

  if (notes) {
    return (
      <Modal title={t.wizard.created(notes.added.branch)} onClose={onClose} drawer>
        {notes.startFailed ? <Banner tone="error" title={t.wizard.startError} role="alert"><p>{notes.startFailed}</p><p>{t.detail.keptNote}</p></Banner> : null}
        {(notes.added.notes ?? []).length > 0 ? <Banner tone="info" title={t.wizard.notesTitle}><ul>{(notes.added.notes ?? []).map((x, i) => <li key={i}>{x}</li>)}</ul></Banner> : null}
        <p class="mono small">{notes.added.path}</p>
        <div class="dialog-actions">
          <button type="button" class="btn" onClick={onClose}>{t.app.close}</button>
          {notes.id ? <a class="btn primary" href={href.detail(project, notes.id)}>{t.wizard.gotoDetail}</a> : null}
        </div>
      </Modal>
    );
  }

  const portLine = !main ? null : plan.loading ? <Loading label={t.wizard.s3Loading} /> : plan.error ? <p class="hint">{t.wizard.previewError}</p>
    : clashes.length === 0 ? <p class="hint ok-text">✓ {hasFixedPorts(plan.data ?? undefined) ? t.wizard.s3NoConflict : t.wizard.s3NoPorts}</p>
    : (
      <div class="field">
        <p class="hint warn-text" id="ni-ports-desc">{n.portsTaken(clashes.map((c) => `${c.port} (${c.service})`).join(", "))}</p>
        <div class="check">
          <input id="ni-auto" type="checkbox" checked={autoPorts} aria-describedby="ni-ports-desc" onChange={(e) => setAutoPorts((e.currentTarget as HTMLInputElement).checked)} />
          <label for="ni-auto">{n.autoPorts}</label>
        </div>
      </div>
    );

  return (
    <Modal title={n.title(project)} onClose={onClose} drawer>
      <form onSubmit={(e) => { e.preventDefault(); void create(true); }} noValidate>
        <fieldset>
          <legend class="sr-only">{n.modeLegend}</legend>
          <div class="segmented">
            <input id="ni-mode-new" type="radio" name="ni-mode" checked={mode === "new"} onChange={() => { setMode("new"); setErr(""); }} />
            <label for="ni-mode-new">{n.modeNew}</label>
            <input id="ni-mode-ex" type="radio" name="ni-mode" checked={mode === "existing"} onChange={() => { setMode("existing"); setErr(""); }} />
            <label for="ni-mode-ex">{n.modeExisting}</label>
          </div>
        </fieldset>
        {mode === "new" ? (
          <>
            <Field id="ni-name" label={n.name} hint={n.nameHint} error={err}>
              <input id="ni-name" ref={nameRef} class="input mono" value={name} autocomplete="off" spellcheck={false} autoFocus placeholder={n.branchPlaceholder}
                onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} {...describedBy("ni-name", n.nameHint, err)} />
            </Field>
            <Field id="ni-from" label={t.wizard.from}>
              <select id="ni-from" class="input" value={from} onChange={(e) => setFrom((e.currentTarget as HTMLSelectElement).value)}>
                {list.filter((b) => !b.remote).map((b) => <option key={b.name} value={b.name}>{b.name}</option>)}
              </select>
            </Field>
          </>
        ) : (
          <Field id="ni-branch" label={t.wizard.branch} hint={t.wizard.branchHint} error={err}>
            <select id="ni-branch" class="input mono" value={branch} onChange={(e) => { setBranch((e.currentTarget as HTMLSelectElement).value); setErr(""); }} {...describedBy("ni-branch", t.wizard.branchHint, err)}>
              <option value="">{n.pickBranch}</option>
              {list.map((b) => <option key={b.name + (b.remote ? "r" : "")} value={b.name} disabled={!!b.worktree}>{b.name}{b.remote ? t.wizard.remoteSuffix : ""}{b.worktree ? ` — ${t.wizard.inUse(b.worktree)}` : ""}</option>)}
            </select>
          </Field>
        )}
        {portLine}
        <details class="more">
          <summary>{n.more}</summary>
          <Field id="ni-owner" label={n.owner} hint={n.ownerHint}>
            <input id="ni-owner" class="input" value={owner} autocomplete="off" onInput={(e) => setOwner((e.currentTarget as HTMLInputElement).value)} {...describedBy("ni-owner", n.ownerHint)} />
          </Field>
          <Field id="ni-folder" label={t.wizard.folder} hint={t.wizard.folderHint(proposal)}>
            <input id="ni-folder" class="input mono" value={folder} placeholder={proposal} autocomplete="off" spellcheck={false} onInput={(e) => setFolder((e.currentTarget as HTMLInputElement).value)} {...describedBy("ni-folder", t.wizard.folderHint(proposal))} />
          </Field>
          {copyPatterns.length > 0 ? (
            <div class="check field">
              <input id="ni-copy" type="checkbox" checked={copy} onChange={(e) => setCopy((e.currentTarget as HTMLInputElement).checked)} />
              <label for="ni-copy">{n.copy(copyPatterns.join(", "))}</label>
            </div>
          ) : null}
          <div class="check field">
            <input id="ni-clone" type="checkbox" checked={clone} aria-describedby="ni-clone-hint" onChange={(e) => setClone((e.currentTarget as HTMLInputElement).checked)} />
            <div><label for="ni-clone">{n.clone}</label><p class="hint" id="ni-clone-hint">{n.cloneHint}</p></div>
          </div>
          {fixed.length > 0 ? (
            <div class="check field">
              <input id="ni-neutralize" type="checkbox" checked={neutralize} aria-describedby="ni-fixed" onChange={(e) => setNeutralize((e.currentTarget as HTMLInputElement).checked)} />
              <div><label for="ni-neutralize">{t.wizard.neutralize}</label><p class="hint" id="ni-fixed">{t.wizard.fixedNamesBody(fixed.map(([s, x]) => `${s}: ${x}`).join(", "))}</p></div>
            </div>
          ) : null}
          <p class="hint">{info?.env_file ? t.wizard.envProject(info.env_file) : t.wizard.envMain}</p>
        </details>
        <p class="summary-line" aria-live="off">{chosen ? n.summary(chosen, mode === "new", from || "HEAD", folder.trim() || proposal) : n.summaryEmpty}</p>
        <ErrorBanner error={failure} title={t.wizard.createError} />
        <div class="dialog-actions spread">
          <CopyCommand command={command} />
          <span class="grow" />
          <button type="button" class="btn quiet" onClick={onClose} disabled={busy}>{t.app.cancel}</button>
          <button type="button" class="btn" onClick={() => void create(false)} disabled={busy}>{n.createOnly}</button>
          <button type="submit" class="btn primary" disabled={busy}>{busy ? t.wizard.creating : n.createAndStart}</button>
        </div>
      </form>
    </Modal>
  );
}
