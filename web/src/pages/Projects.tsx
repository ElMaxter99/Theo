import { useEffect, useState } from "preact/hooks";
import { api, projectPath } from "../api/client.ts";
import type { Meta, Overview, Project } from "../api/types.ts";
import { Banner, ErrorBanner, Field, Loading, PageHead, describedBy } from "../components/ui.tsx";
import { Badge } from "../components/ui.tsx";
import { ConfirmDialog, Modal } from "../components/Dialog.tsx";
import { Icon } from "../components/Icon.tsx";
import { brand } from "../brand.ts";
import { useAction, useResource } from "../lib/hooks.ts";
import { href, navigate } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { rememberWorktrees } from "../ops/store.ts";
import { stateView } from "../lib/status.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function Projects({ meta }: { meta: Meta | undefined }) {
  const res = useResource<Project[]>(() => api.get<Project[]>("/projects"), [], {});
  const [removing, setRemoving] = useState<Project | null>(null);
  const [editing, setEditing] = useState<Project | null>(null);
  useEffect(() => setTitle([t.projects.title]), []);

  if (res.loading && !res.data) return <><PageHead title={t.projects.title} /><Loading /></>;
  if (res.error && !res.data) return <><PageHead title={t.projects.title} /><ErrorBanner error={res.error} title={t.projects.loadError} /></>;
  const projects = res.data ?? [];
  if (projects.length === 0) return <Welcome meta={meta} />;

  return (
    <>
      <PageHead title={t.projects.title} actions={<a class="btn primary" href={href.add()}><Icon name="plus" /> {t.projects.add}</a>} />
      <ul class="cards">
        {projects.map((p) => <ProjectCard key={p.name} project={p} onRemove={() => setRemoving(p)} onSettings={() => setEditing(p)} />)}
      </ul>
      {editing ? <ProjectSettings project={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); res.reload(); }} /> : null}
      {removing ? (
        <ConfirmDialog
          title={t.projects.unregisterTitle(removing.name)}
          body={t.projects.unregisterBody}
          confirmLabel={t.projects.unregisterConfirm}
          onClose={() => setRemoving(null)}
          onConfirm={async () => {
            await api.del(projectPath(removing.name));
            announce(t.projects.unregistered(removing.name));
            res.reload();
          }}
        />
      ) : null}
    </>
  );
}

function ProjectCard({ project, onRemove, onSettings }: { project: Project; onRemove: () => void; onSettings: () => void }) {
  const ov = useResource<Overview>(() => api.get<Overview>(`${projectPath(project.name)}/overview`), [project.name], { project: project.name });
  useEffect(() => { if (ov.data) rememberWorktrees(ov.data.worktrees); }, [ov.data]);
  const wts = ov.data?.worktrees ?? [];
  const running = wts.filter((w) => w.state === "running").length;
  const unknown = ov.data ? !ov.data.docker.available || wts.some((w) => w.state === "unknown") : false;
  return (
    <li class="card">
      <h2><a href={href.project(project.name)}>{project.name}</a></h2>
      <p class="mono small muted">{project.path}</p>
      <p class="row">
        {ov.data ? (
          <>
            <span>{t.projects.worktrees(wts.length)}</span>
            <span aria-hidden="true">·</span>
            {unknown ? <Badge view={stateView("unknown")} /> : <Badge view={running > 0 ? stateView("running") : stateView("stopped")} extra={t.projects.running(running)} />}
            <span>{unknown ? t.projects.stateUnknown : running > 0 ? t.projects.running(running) : t.projects.noInstances}</span>
          </>
        ) : ov.error ? <span class="muted">{ov.error.message}</span> : <span class="muted">{t.app.loading}</span>}
      </p>
      {project.env_file ? <p class="small muted">{t.projects.envFile}: <span class="mono">{project.env_file}</span></p> : null}
      <div class="row">
        <a class="btn primary sm" href={href.project(project.name)}>{t.projects.open}<span class="sr-only"> {project.name}</span></a>
        <button type="button" class="btn sm" onClick={onSettings} aria-label={`${t.projects.settings} ${project.name}`}>{t.projects.settings}</button>
        <button type="button" class="btn sm" onClick={onRemove} aria-label={`${t.projects.unregister} ${project.name}`}>{t.projects.unregister}</button>
      </div>
    </li>
  );
}

function Welcome({ meta }: { meta: Meta | undefined }) {
  const blocked = meta && (!meta.docker.available || !meta.git.available);
  return (
    <div class="welcome">
      <img class="mascot" src={brand.mascot} alt={t.welcome.mascotAlt} />
      <div>
        <h1 tabIndex={-1}>{t.welcome.title}</h1>
        <p>{t.welcome.lead}</p>
        {blocked ? <Banner tone="warn"><p>{t.banner.dockerBody}</p></Banner> : null}
        <p><a class="btn primary" href={href.add()} onClick={(e) => { e.preventDefault(); navigate(href.add()); }}>{t.welcome.add}</a></p>
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

function ProjectSettings({ project, onClose, onSaved }: { project: Project; onClose: () => void; onSaved: () => void }) {
  const [env, setEnv] = useState(project.env_file ?? "");
  const [dir, setDir] = useState(project.worktree_dir ?? "");
  const save = useAction(async () => {
    // An empty field clears the setting (null); only changed fields are sent.
    const body: Record<string, string | null> = {};
    if (env.trim() !== (project.env_file ?? "")) body.env_file = env.trim() || null;
    if (dir.trim() !== (project.worktree_dir ?? "")) body.worktree_dir = dir.trim() || null;
    if (Object.keys(body).length > 0) await api.patch(projectPath(project.name), body);
    announce(t.projects.settingsSaved);
    onSaved();
  });
  return (
    <Modal title={t.projects.settingsTitle(project.name)} onClose={onClose}>
      <form onSubmit={(e) => { e.preventDefault(); void save.run(); }}>
        <Field id="ps-env" label={t.projects.settingsEnv} hint={t.projects.settingsEnvHint}>
          <input id="ps-env" class="input mono" value={env} autocomplete="off" spellcheck={false} onInput={(e) => setEnv((e.currentTarget as HTMLInputElement).value)} {...describedBy("ps-env", t.projects.settingsEnvHint)} />
        </Field>
        <Field id="ps-dir" label={t.projects.settingsDir} hint={t.projects.settingsDirHint}>
          <input id="ps-dir" class="input mono" value={dir} autocomplete="off" spellcheck={false} onInput={(e) => setDir((e.currentTarget as HTMLInputElement).value)} {...describedBy("ps-dir", t.projects.settingsDirHint)} />
        </Field>
        <ErrorBanner error={save.error} />
        <div class="dialog-actions">
          <button type="button" class="btn" onClick={onClose}>{t.app.cancel}</button>
          <button type="submit" class="btn primary" disabled={save.busy}>{t.app.save}</button>
        </div>
      </form>
    </Modal>
  );
}
