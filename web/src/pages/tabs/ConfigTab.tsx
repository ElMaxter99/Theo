// V-05 Instance › Configuration: what Theo keeps for this instance (profiles, owner, ports, container names), its
// variables, and the configuration Docker Compose would run with.
import { useEffect, useState } from "preact/hooks";
import { api, qs, wtPath } from "../../api/client.ts";
import type { WorktreeDetail, WorktreeSettingsInfo } from "../../api/types.ts";
import { ErrorBanner, Loading } from "../../components/ui.tsx";
import { CopyCommand, theoCmd } from "../../components/CopyCommand.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { announce } from "../../lib/live.ts";
import { t } from "../../strings.ts";
import { VariablesTab } from "./VariablesTab.tsx";
import { wtArg } from "../ProjectPanel.tsx";

export function ConfigTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const base = `${wtPath(project, w.id)}/settings`;
  const settings = useResource<WorktreeSettingsInfo>(() => api.get<WorktreeSettingsInfo>(base), [project, w.id], { project, worktree: w.id });
  const s = settings.data;
  const [owner, setOwner] = useState("");
  const [preview, setPreview] = useState(false);
  useEffect(() => { setOwner(s?.owner ?? ""); }, [s?.owner]);
  const save = useAction(async (body: Record<string, unknown>) => {
    await api.patch(base, body);
    announce(t.summary.settingSaved);
    settings.reload();
  });
  const known = s?.known_profiles ?? [];
  const on = new Set(s?.profiles ?? []);
  const arg = wtArg(w);
  const c = t.config;

  return (
    <div class="stack">
      <section class="card stack" aria-labelledby="cfg-settings">
        <h2 id="cfg-settings">{c.settingsTitle}</h2>
        {settings.loading && !s ? <Loading /> : null}
        <ErrorBanner error={settings.error ?? save.error} />
        {s ? (
          <>
            <fieldset>
              <legend>{c.profiles}</legend>
              {known.length === 0 ? <p class="hint">{c.noProfiles}</p> : (
                <>
                  <p class="hint" id="cfg-prof-hint">{c.profilesHint}</p>
                  <div class="row">
                    {known.map((p) => (
                      <div class="check" key={p}>
                        <input id={`cfg-p-${p}`} type="checkbox" checked={on.has(p)} disabled={save.busy} aria-describedby="cfg-prof-hint"
                          onChange={(e) => { const n = new Set(on); if ((e.currentTarget as HTMLInputElement).checked) n.add(p); else n.delete(p); void save.run({ profiles: [...n] }); }} />
                        <label for={`cfg-p-${p}`} class="mono">{p}</label>
                      </div>
                    ))}
                  </div>
                </>
              )}
            </fieldset>
            <form class="field" onSubmit={(e) => { e.preventDefault(); void save.run({ owner: owner.trim() }); }}>
              <label for="cfg-owner">{c.owner}</label>
              <div class="row">
                <input id="cfg-owner" class="input auto" value={owner} aria-describedby="cfg-owner-hint" onInput={(e) => setOwner((e.currentTarget as HTMLInputElement).value)} />
                <button type="submit" class="btn sm" disabled={save.busy || owner.trim() === (s.owner ?? "")}>{t.app.save}</button>
              </div>
              <p class="hint" id="cfg-owner-hint">{c.ownerHint}</p>
            </form>
            <div class="check">
              <input id="cfg-auto" type="checkbox" checked={!!s.auto_ports} disabled={save.busy} aria-describedby="cfg-auto-hint" onChange={(e) => void save.run({ auto_ports: (e.currentTarget as HTMLInputElement).checked })} />
              <div><label for="cfg-auto">{c.autoPorts}</label><p class="hint" id="cfg-auto-hint">{c.autoPortsHint}</p></div>
            </div>
            {Object.keys(s.fixed_container_names ?? {}).length > 0 || s.neutralize_container_names ? (
              <div class="check">
                <input id="neutralize" type="checkbox" checked={!!s.neutralize_container_names} disabled={save.busy} aria-describedby="neutralize-hint"
                  onChange={(e) => void save.run({ neutralize_container_names: (e.currentTarget as HTMLInputElement).checked })} />
                <div><label for="neutralize">{t.summary.neutralize}</label><p class="hint" id="neutralize-hint">{t.summary.neutralizeHint}</p></div>
              </div>
            ) : null}
            <p class="hint">{c.appliesNext} <CopyCommand command={theoCmd("instance", "show", project, arg ? "-w" : "", arg)} /></p>
          </>
        ) : null}
      </section>

      <section aria-labelledby="cfg-vars">
        <h2 id="cfg-vars">{t.vars.title}</h2>
        <VariablesTab project={project} w={w} />
      </section>

      <section class="stack" aria-labelledby="cfg-preview">
        <h2 id="cfg-preview">{c.previewTitle}</h2>
        <p class="hint">{c.previewHint}</p>
        <div><button type="button" class="btn sm" aria-expanded={preview} onClick={() => setPreview(!preview)}>{preview ? c.hidePreview : c.showPreview}</button></div>
        {preview ? <ConfigPreview path={`${wtPath(project, w.id)}/config${qs({ auto_ports: s?.auto_ports ? true : undefined })}`} /> : null}
      </section>
    </div>
  );
}

function ConfigPreview({ path }: { path: string }) {
  const res = useResource<string>(() => api.get<string>(path), [path]);
  if (res.loading && res.data === undefined) return <Loading />;
  if (res.error) return <ErrorBanner error={res.error} />;
  return <pre class="log-view static mono small" tabIndex={0} aria-label={t.config.previewTitle}>{res.data}</pre>;
}
