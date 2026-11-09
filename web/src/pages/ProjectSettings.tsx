// V-07 Project › Settings: name, Compose files, env file, where worktrees go, files copied into new worktrees, the
// paragraph for the repository's AGENTS.md, and unregistering the project.
import { useEffect, useState } from "preact/hooks";
import { api, projectPath, qs } from "../api/client.ts";
import type { McpInstall, Project, RenameResult } from "../api/types.ts";
import { ErrorBanner, Field, Loading, describedBy } from "../components/ui.tsx";
import { ConfirmDialog, TypedConfirmDialog } from "../components/Dialog.tsx";
import { CopyCommand, copyText, theoCmd } from "../components/CopyCommand.tsx";
import { useAction, useResource } from "../lib/hooks.ts";
import { announce } from "../lib/live.ts";
import { href, navigate } from "../lib/router.ts";
import { refreshStatus } from "../lib/statusStore.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";
import { ProjectHeader } from "./ProjectHeader.tsx";

const lines = (s: string): string[] => s.split(/\r?\n|,/).map((x) => x.trim()).filter(Boolean);

export function ProjectSettings({ project }: { project: string }) {
  const res = useResource<Project[]>(() => api.get<Project[]>("/projects"), [project], { project });
  const p = res.data?.find((x) => x.name === project);
  const ps = t.projectSettings;
  const [name, setName] = useState(project);
  const [files, setFiles] = useState("");
  const [envFile, setEnvFile] = useState("");
  const [dir, setDir] = useState("");
  const [copyOn, setCopyOn] = useState("");
  const [urlMode, setUrlMode] = useState("auto");
  const [renaming, setRenaming] = useState(false);
  const [removing, setRemoving] = useState(false);
  useEffect(() => setTitle([t.projectTabs.settings, project]), [project]);
  useEffect(() => {
    if (!p) return;
    setName(p.name);
    setFiles((p.compose_files ?? []).join("\n"));
    setEnvFile(p.env_file ?? "");
    setDir(p.worktree_dir ?? "");
    setCopyOn((p.copy_on_add ?? []).join("\n"));
    setUrlMode(p.url_in_data ?? "auto");
  }, [p?.name, p?.compose_files?.join("|"), p?.env_file, p?.worktree_dir, p?.copy_on_add?.join("|"), p?.url_in_data]);

  const save = useAction(async () => {
    await api.patch(projectPath(project), {
      compose_files: lines(files).length > 0 ? lines(files) : null,
      env_file: envFile.trim() || null,
      worktree_dir: dir.trim() || null,
      copy_on_add: lines(copyOn).length > 0 ? lines(copyOn) : null,
      url_in_data: urlMode === "auto" ? null : urlMode,
    });
    announce(ps.saved);
    res.reload();
  });
  const snippet = useResource<McpInstall>(() => api.get<McpInstall>(`/mcp/install${qs({})}`), []);

  if (res.loading && !res.data) return <><ProjectHeader project={project} tab="settings" /><Loading hero /></>;
  if (!p) return <><ProjectHeader project={project} tab="settings" /><ErrorBanner error={res.error ?? { message: t.panel.notFound }} /></>;

  return (
    <>
      <ProjectHeader project={project} path={p.path} tab="settings" />
      <form class="card stack" aria-labelledby="ps-general" onSubmit={(e) => { e.preventDefault(); void save.run(); }}>
        <h2 id="ps-general">{ps.general}</h2>
        <Field id="ps-files" label={ps.composeFiles} hint={ps.composeFilesHint}>
          <textarea id="ps-files" class="input mono" rows={3} value={files} placeholder="compose.yaml" onInput={(e) => setFiles((e.currentTarget as HTMLTextAreaElement).value)} {...describedBy("ps-files", ps.composeFilesHint)} />
        </Field>
        <Field id="ps-env" label={t.addProject.envFile} hint={ps.envHint}>
          <input id="ps-env" class="input mono" value={envFile} onInput={(e) => setEnvFile((e.currentTarget as HTMLInputElement).value)} {...describedBy("ps-env", ps.envHint)} />
        </Field>
        <Field id="ps-dir" label={ps.worktreeDir} hint={ps.worktreeDirHint(p.worktree_dir_effective ?? "")}>
          <input id="ps-dir" class="input mono" value={dir} placeholder={p.worktree_dir_effective} onInput={(e) => setDir((e.currentTarget as HTMLInputElement).value)} {...describedBy("ps-dir", ps.worktreeDirHint(p.worktree_dir_effective ?? ""))} />
        </Field>
        <Field id="ps-copy" label={ps.copyOnAdd} hint={ps.copyOnAddHint}>
          <textarea id="ps-copy" class="input mono" rows={3} value={copyOn} placeholder=".env.local" onInput={(e) => setCopyOn((e.currentTarget as HTMLTextAreaElement).value)} {...describedBy("ps-copy", ps.copyOnAddHint)} />
        </Field>
        <Field id="ps-url" label={ps.urlInData} hint={ps.urlInDataHint}>
          <select id="ps-url" class="input auto" value={urlMode} onChange={(e) => setUrlMode((e.currentTarget as HTMLSelectElement).value)} {...describedBy("ps-url", ps.urlInDataHint)}>
            {["auto", "on", "off"].map((m) => <option key={m} value={m}>{ps.urlInDataOptions[m]}</option>)}
          </select>
        </Field>
        <ErrorBanner error={save.error} />
        <div class="row">
          <button type="submit" class="btn primary" disabled={save.busy}>{t.app.save}</button>
          <CopyCommand command={theoCmd("project", "show", project)} />
        </div>
      </form>

      <section class="card stack" aria-labelledby="ps-name">
        <h2 id="ps-name">{ps.renameTitle}</h2>
        <p class="hint">{ps.renameHint}</p>
        <div class="row">
          <label for="ps-newname" class="sr-only">{ps.newName}</label>
          <input id="ps-newname" class="input auto" value={name} onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
          <button type="button" class="btn sm" disabled={!name.trim() || name.trim() === project} onClick={() => setRenaming(true)}>{ps.rename}</button>
        </div>
      </section>

      <section class="card stack" aria-labelledby="ps-agents">
        <h2 id="ps-agents">{ps.agentsTitle}</h2>
        <p class="hint">{ps.agentsHint}</p>
        {snippet.data ? (
          <>
            <pre class="snippet mono small" tabIndex={0} aria-label={ps.agentsTitle}>{snippet.data.snippet}</pre>
            <div class="row">
              <button type="button" class="btn sm" onClick={() => { void copyText(snippet.data!.snippet).then((ok) => announce(ok ? t.copy.copied : t.copy.failed)); }}>{ps.copySnippet}</button>
              <a class="btn sm quiet" href={href.settings("agents")}>{ps.agentsSettings}</a>
            </div>
          </>
        ) : snippet.loading ? <Loading /> : <ErrorBanner error={snippet.error} />}
      </section>

      <section class="card stack danger-zone" aria-labelledby="ps-remove">
        <h2 id="ps-remove">{ps.removeTitle}</h2>
        <p>{t.projects.unregisterBody}</p>
        <div><button type="button" class="btn danger" onClick={() => setRemoving(true)}>{t.projects.unregister}</button></div>
      </section>

      {renaming ? (
        <TypedConfirmDialog
          title={ps.renameConfirmTitle(project, name.trim())}
          lead={ps.renameLead}
          expected={project}
          preservedTitle={t.confirm.preserved}
          preserved={[ps.keepFiles, ps.keepVolumesNote]}
          confirmLabel={ps.rename}
          onClose={() => setRenaming(false)}
          onConfirm={async () => {
            const r = await api.post<RenameResult>(`${projectPath(project)}/rename`, { name: name.trim(), yes: true });
            announce(ps.renamed(r.new));
            void refreshStatus();
            navigate(href.projectSettings(r.new), true);
          }}
        />
      ) : null}
      {removing ? (
        <ConfirmDialog
          title={t.projects.unregisterTitle(project)}
          body={t.projects.unregisterBody}
          confirmLabel={t.projects.unregisterConfirm}
          onClose={() => setRemoving(false)}
          onConfirm={async () => {
            await api.del(projectPath(project));
            announce(t.projects.unregistered(project));
            void refreshStatus();
            navigate(href.projects());
          }}
        />
      ) : null}
    </>
  );
}
