import { useEffect, useState } from "preact/hooks";
import { api, qs } from "../api/client.ts";
import type { Browse, Project } from "../api/types.ts";
import { Modal } from "../components/Dialog.tsx";
import { Icon } from "../components/Icon.tsx";
import { Crumbs, ErrorBanner, Field, Loading, PageHead, describedBy } from "../components/ui.tsx";
import { useAction, useResource } from "../lib/hooks.ts";
import { suggestName } from "../lib/format.ts";
import { announce } from "../lib/live.ts";
import { href, navigate } from "../lib/router.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function AddProject() {
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [envFile, setEnvFile] = useState("");
  const [browse, setBrowse] = useState(false);
  const [errs, setErrs] = useState<{ path?: string; name?: string }>({});
  useEffect(() => setTitle([t.addProject.title]), []);

  const submit = useAction(async () => {
    const body: Record<string, string> = { path: path.trim() };
    if (name.trim()) body.name = name.trim();
    if (envFile.trim()) body.env_file = envFile.trim();
    const p = await api.post<Project>("/projects", body);
    announce(t.addProject.added(p.name));
    navigate(href.project(p.name));
  });

  const onPath = (v: string) => {
    setPath(v);
    if (!nameTouched) setName(suggestName(v));
  };
  const onSubmit = (e: Event) => {
    e.preventDefault();
    const next: { path?: string; name?: string } = {};
    if (!path.trim()) next.path = t.addProject.pathRequired;
    if (!name.trim()) next.name = t.addProject.nameRequired;
    setErrs(next);
    if (next.path || next.name) {
      document.getElementById(next.path ? "ap-path" : "ap-name")?.focus();
      return;
    }
    void submit.run();
  };

  return (
    <>
      <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: t.addProject.title }]} />
      <PageHead title={t.addProject.title} sub={t.addProject.lead} />
      <form onSubmit={onSubmit} noValidate>
        <Field id="ap-path" label={t.addProject.path} hint={t.addProject.pathHint} error={errs.path}>
          <div class="row">
            <input id="ap-path" class="input mono" value={path} autocomplete="off" spellcheck={false} onInput={(e) => onPath((e.currentTarget as HTMLInputElement).value)} {...describedBy("ap-path", t.addProject.pathHint, errs.path)} />
            <button type="button" class="btn" onClick={() => setBrowse(true)}><Icon name="folder" /> {t.addProject.browse}</button>
          </div>
        </Field>
        <Field id="ap-name" label={t.addProject.name} hint={t.addProject.nameHint} error={errs.name}>
          <input id="ap-name" class="input" value={name} autocomplete="off" spellcheck={false} onInput={(e) => { setNameTouched(true); setName((e.currentTarget as HTMLInputElement).value); }} {...describedBy("ap-name", t.addProject.nameHint, errs.name)} />
        </Field>
        <Field id="ap-env" label={t.addProject.envFile} hint={t.addProject.envHint}>
          <input id="ap-env" class="input mono" value={envFile} autocomplete="off" spellcheck={false} onInput={(e) => setEnvFile((e.currentTarget as HTMLInputElement).value)} {...describedBy("ap-env", t.addProject.envHint)} />
        </Field>
        <ErrorBanner error={submit.error} />
        <div class="row">
          <button type="submit" class="btn primary" disabled={submit.busy}>{t.addProject.submit}</button>
          <a class="btn" href={href.projects()}>{t.app.cancel}</a>
        </div>
      </form>
      {browse ? <BrowseDialog start={path.trim()} onClose={() => setBrowse(false)} onPick={(p) => { onPath(p); setBrowse(false); }} /> : null}
    </>
  );
}

function BrowseDialog({ start, onClose, onPick }: { start: string; onClose: () => void; onPick: (p: string) => void }) {
  const [dir, setDir] = useState(start);
  const res = useResource<Browse>(() => api.get<Browse>(`/fs/browse${qs({ path: dir })}`), [dir]);
  return (
    <Modal title={t.addProject.browseTitle} onClose={onClose} wide>
      <p class="hint">{t.addProject.browseNote}</p>
      {res.loading && !res.data ? <Loading /> : null}
      <ErrorBanner error={res.error} />
      {res.data ? (
        <>
          <p><strong>{t.addProject.browseCurrent}:</strong> <span class="mono">{res.data.path}</span></p>
          <ul class="affected" aria-label={res.data.path}>
            {res.data.parent ? <li><button type="button" class="btn quiet sm" onClick={() => setDir(res.data!.parent!)}><Icon name="back" /> {t.addProject.browseUp}</button></li> : null}
            {res.data.entries.length === 0 ? <li class="muted">{t.addProject.browseEmpty}</li> : null}
            {res.data.entries.map((e) => (
              <li key={e.path}>
                <button type="button" class="btn quiet sm" aria-label={t.addProject.browseOpen(e.name)} onClick={() => setDir(e.path)}>
                  <Icon name={e.is_repo ? "git" : "folder"} /> {e.name}
                </button>
                {e.is_repo ? <span class="tag">{t.addProject.browseRepo}</span> : null}
              </li>
            ))}
          </ul>
        </>
      ) : null}
      <div class="dialog-actions">
        <button type="button" class="btn" onClick={onClose}>{t.app.cancel}</button>
        <button type="button" class="btn primary" disabled={!res.data} onClick={() => res.data && onPick(res.data.path)}>{t.addProject.browseUse}</button>
      </div>
    </Modal>
  );
}
