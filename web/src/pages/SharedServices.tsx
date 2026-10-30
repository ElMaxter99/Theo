// Shared services of a project (view-phase 2.5): provider state, consumers, add / remove, start / stop (operations),
// link / unlink an instance, purge (level 3). Every confirmation text comes from the server; the server validates again.
import { useEffect, useMemo, useState } from "preact/hooks";
import { api, ApiError, projectPath, wtPath } from "../api/client.ts";
import type { ApiErrorBody, Overview, SharedConsumer, SharedService, SharedStatus, Worktree } from "../api/types.ts";
import { Badge, Banner, Crumbs, ErrorBanner, Field, Loading, PageHead } from "../components/ui.tsx";
import { ConfirmDialog, Modal, TypedConfirmDialog } from "../components/Dialog.tsx";
import { Icon } from "../components/Icon.tsx";
import { useAction, useResource } from "../lib/hooks.ts";
import { announce } from "../lib/live.ts";
import { href } from "../lib/router.ts";
import { isActiveOp } from "../lib/status.ts";
import type { StatusView } from "../lib/status.ts";
import { basename } from "../lib/format.ts";
import { startSharedOperation, useOps } from "../ops/store.ts";
import { normKind } from "../ops/kind.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function sharedStateView(state: string): StatusView {
  switch (state) {
    case "running": return { label: t.shared.stateRunning, icon: "running", tone: "ok" };
    case "not created": return { label: t.shared.stateNotCreated, icon: "absent", tone: "neutral" };
    case "exited": case "created": case "paused": return { label: t.shared.stateStopped, icon: "stopped", tone: "neutral" };
    default: return { label: t.shared.stateOther(state), icon: "unknown", tone: "neutral" };
  }
}

type Dialogs =
  | { kind: "down"; body: ApiErrorBody }
  | { kind: "purge"; body: ApiErrorBody }
  | { kind: "inuse"; body: ApiErrorBody }
  | { kind: "remove"; service: string };

const asError = (e: unknown): ApiError => (e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));

export function SharedServices({ project }: { project: string }) {
  const base = `${projectPath(project)}/shared`;
  const res = useResource<SharedStatus | null>(async () => {
    try {
      return await api.get<SharedStatus>(base);
    } catch (e) {
      if (e instanceof ApiError && e.code === "shared_not_configured") return null;
      throw e;
    }
  }, [project], { project });
  const overview = useResource<Overview>(() => api.get<Overview>(`${projectPath(project)}/overview`), [project], { project });
  const ops = useOps();
  const [dialog, setDialog] = useState<Dialogs | null>(null);
  useEffect(() => setTitle([t.shared.title, project]), [project]);

  // The provider's own operations (start / stop) refresh the page when they end.
  const activeShared = ops.filter((o) => o.project === project && /^shared/.test(normKind(o.kind)) && isActiveOp(o));
  const activeKey = ops.filter((o) => o.project === project && /^shared/.test(normKind(o.kind))).map((o) => o.id + o.state).join(",");
  useEffect(() => { if (res.data !== undefined) res.reload(); /* eslint-disable-next-line */ }, [activeKey]);

  const crumbs = <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project, href: href.project(project) }, { label: t.shared.crumb }]} />;
  const reloadAll = () => { res.reload(); overview.reload(); };

  const startStop = useAction(async (kind: "up" | "down") => {
    try {
      await startSharedOperation(project, kind, {});
      announce(kind === "up" ? t.shared.startedMsg : t.shared.stoppedMsg);
    } catch (e) {
      if (kind === "down" && e instanceof ApiError && e.code === "confirmation_required") { setDialog({ kind: "down", body: e.body }); return; }
      throw e;
    }
  });

  const purge = useAction(async () => {
    try {
      await api.post(`${base}/purge`, {});
      announce(t.shared.purged);
      reloadAll();
    } catch (e) {
      if (e instanceof ApiError && e.code === "confirmation_required") { setDialog({ kind: "purge", body: e.body }); return; }
      if (e instanceof ApiError && e.code === "shared_in_use") { setDialog({ kind: "inuse", body: e.body }); return; }
      throw e;
    }
  });

  if (res.loading && res.data === undefined) return <>{crumbs}<PageHead title={t.shared.title} /><Loading /></>;
  if (res.error && res.data === undefined) {
    return <>{crumbs}<PageHead title={t.shared.title} /><ErrorBanner error={res.error} title={res.error.status === 404 ? t.panel.notFound : t.shared.loadError} /></>;
  }
  const st = res.data ?? null;
  const worktrees = overview.data?.worktrees ?? [];

  return (
    <>
      {crumbs}
      <PageHead
        title={t.shared.title}
        sub={<span class="mono">{project}</span>}
        actions={<button type="button" class="btn" onClick={reloadAll}><Icon name="restart" /> {t.shared.refresh}</button>}
      />
      <p>{t.shared.intro}</p>
      <ErrorBanner error={startStop.error ?? purge.error} />
      {st === null ? (
        <Empty project={project} worktrees={worktrees} onChanged={reloadAll} />
      ) : (
        <>
          {(st.warnings ?? []).length > 0 ? (
            <Banner tone="warn" title={t.shared.warnings}><ul>{st.warnings!.map((w, i) => <li key={i}>{w}</li>)}</ul></Banner>
          ) : null}
          <Provider
            project={project} st={st} busy={activeShared.length > 0}
            onStart={() => void startStop.run("up")} onStop={() => void startStop.run("down")}
            onPurge={() => void purge.run()} onRemove={(service) => setDialog({ kind: "remove", service })}
          />
          <Consumers project={project} st={st} worktrees={worktrees} onChanged={reloadAll} />
          <AddService project={project} worktrees={worktrees} onChanged={reloadAll} heading={t.shared.addTitle} existing={st.services.map((s) => s.name)} />
        </>
      )}

      {dialog?.kind === "down" ? (
        <TypedConfirmDialog
          title={t.shared.downTitle}
          lead={dialog.body.message || t.shared.downBody}
          expected={dialog.body.confirm?.expected ?? `${project}-shared`}
          prompt={dialog.body.confirm?.prompt}
          affected={dialog.body.affected?.length ? dialog.body.affected : (st?.consumers ?? []).map((c) => ({ kind: "instance", name: c.branch || basename(c.workspace), detail: c.active ? t.shared.active : t.shared.inactive }))}
          affectedTitle={t.shared.downAffected}
          preserved={dialog.body.preserved ?? [t.shared.downBody]}
          confirmLabel={t.shared.downConfirm}
          onClose={() => setDialog(null)}
          onConfirm={async (text) => { await startSharedOperation(project, "down", { confirm: text }); announce(t.shared.stoppedMsg); }}
        />
      ) : null}
      {dialog?.kind === "purge" ? (
        <TypedConfirmDialog
          title={t.shared.purgeTitle(st?.compose_project ?? `${project}-shared`)}
          lead={dialog.body.message}
          expected={dialog.body.confirm?.expected ?? `${project}-shared`}
          prompt={dialog.body.confirm?.prompt}
          affected={dialog.body.affected}
          preserved={dialog.body.preserved}
          confirmLabel={t.shared.purgeConfirm}
          onClose={() => setDialog(null)}
          onConfirm={async (text) => { await api.post(`${base}/purge`, { confirm: text }); announce(t.shared.purged); reloadAll(); }}
        />
      ) : null}
      {dialog?.kind === "inuse" ? (
        <Modal title={t.shared.purgeInUse} onClose={() => setDialog(null)}>
          <p>{dialog.body.message || t.shared.purgeInUseBody}</p>
          {(dialog.body.affected ?? []).length > 0 ? (
            <ul class="affected">{dialog.body.affected!.map((a, i) => <li key={i}><strong class="mono">{a.name}</strong>{a.detail ? <span class="muted"> — {a.detail}</span> : null}</li>)}</ul>
          ) : null}
          <p>{t.shared.purgeInUseBody}</p>
          <div class="dialog-actions"><button type="button" class="btn" autoFocus onClick={() => setDialog(null)}>{t.app.close}</button></div>
        </Modal>
      ) : null}
      {dialog?.kind === "remove" ? (
        <ConfirmDialog
          title={t.shared.removeTitle(dialog.service)}
          body={t.shared.removeBody}
          confirmLabel={t.shared.removeConfirm}
          onClose={() => setDialog(null)}
          onConfirm={async () => { await api.del(`${base}/services/${encodeURIComponent(dialog.service)}`); announce(t.shared.removed(dialog.service)); reloadAll(); }}
        />
      ) : null}
    </>
  );
}

function Empty({ project, worktrees, onChanged }: { project: string; worktrees: Worktree[]; onChanged: () => void }) {
  return (
    <>
      <section class="card" aria-labelledby="sh-empty">
        <h2 id="sh-empty">{t.shared.emptyTitle}</h2>
        <p>{t.shared.emptyBody}</p>
        <p class="small muted">{t.shared.emptyDoc}</p>
      </section>
      <AddService project={project} worktrees={worktrees} onChanged={onChanged} heading={t.shared.addTitle} existing={[]} />
    </>
  );
}

function Provider({ project, st, busy, onStart, onStop, onPurge, onRemove }: {
  project: string; st: SharedStatus; busy: boolean; onStart: () => void; onStop: () => void; onPurge: () => void; onRemove: (s: string) => void;
}) {
  const anyRunning = st.services.some((s) => s.state === "running");
  const allRunning = st.services.length > 0 && st.services.every((s) => s.state === "running");
  return (
    <section class="card stack" aria-labelledby="sh-provider" aria-busy={busy || undefined}>
      <h2 id="sh-provider">{t.shared.providerTitle}</h2>
      <dl class="facts">
        <dt>{t.shared.composeProject}</dt><dd class="mono">{st.compose_project}</dd>
        <dt>{t.shared.network}</dt><dd class="mono">{st.network}</dd>
        <dt>{t.shared.definedIn}</dt><dd class="mono">{st.from ? basename(st.from) : "—"}{st.from ? <span class="small muted"> ({st.from})</span> : null}</dd>
        <dt>{t.shared.ports}</dt>
        <dd><span class={`badge ${st.publish_ports ? "warn" : "neutral"}`}><Icon name={st.publish_ports ? "warn" : "lock"} /> <span>{st.publish_ports ? t.shared.portsPublished : t.shared.portsNotPublished}</span></span></dd>
      </dl>
      <div class="table-wrap" tabIndex={0} role="region" aria-label={t.shared.providerTitle}>
        <table class="data">
          <caption class="sr-only">{t.shared.providerTitle} · {project}</caption>
          <thead><tr><th scope="col">{t.shared.colService}</th><th scope="col">{t.shared.colImage}</th><th scope="col">{t.shared.colState}</th><th scope="col">{t.shared.colActions}</th></tr></thead>
          <tbody>
            {st.services.map((s: SharedService) => (
              <tr key={s.name}>
                <th scope="row" class="mono">{s.name}</th>
                <td class="mono small">{s.image ?? "—"}</td>
                <td><Badge view={busy ? { label: t.state.working, icon: "busy", tone: "info", spin: true } : sharedStateView(s.state)} /></td>
                <td><button type="button" class="btn sm" onClick={() => onRemove(s.name)} aria-label={t.shared.removeNamed(s.name)}>{t.shared.remove}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div class="row">
        <button type="button" class="btn primary" disabled={busy || allRunning || st.services.length === 0} onClick={onStart}><Icon name="play" /> {t.shared.start}</button>
        <button type="button" class="btn" disabled={busy || !anyRunning} onClick={onStop}><Icon name="stop" /> {t.shared.stop}</button>
        <button type="button" class="btn danger" disabled={busy || st.services.length === 0} aria-describedby="sh-purge-hint" onClick={onPurge}>{t.shared.purge}</button>
      </div>
      <p class="hint" id="sh-purge-hint">{t.shared.purgeHint} {t.shared.keptNote}</p>
    </section>
  );
}

/** Which worktree a consumer is: the engine reports the workspace path, the API ids are wt_*. */
function worktreeOf(c: SharedConsumer, worktrees: Worktree[]): Worktree | undefined {
  return worktrees.find((w) => w.path === c.workspace);
}

function Consumers({ project, st, worktrees, onChanged }: { project: string; st: SharedStatus; worktrees: Worktree[]; onChanged: () => void }) {
  const unlink = useAction(async (w: Worktree, service: string) => {
    await api.del(`${wtPath(project, w.id)}/shared/${encodeURIComponent(service)}`);
    announce(t.shared.unlinked(service, w.branch || basename(w.path)));
    onChanged();
  });
  const declared = st.services.map((s) => s.name);
  return (
    <section class="card stack" aria-labelledby="sh-consumers">
      <h2 id="sh-consumers">{t.shared.consumersTitle}</h2>
      <ErrorBanner error={unlink.error} />
      {st.consumers.length === 0 ? <p class="empty">{t.shared.consumersNone}</p> : (
        <div class="table-wrap" tabIndex={0} role="region" aria-label={t.shared.consumersTitle}>
          <table class="data">
            <caption class="sr-only">{t.shared.consumersTitle}</caption>
            <thead><tr><th scope="col">{t.shared.colInstance}</th><th scope="col">{t.shared.colUses}</th><th scope="col">{t.shared.colActive}</th></tr></thead>
            <tbody>
              {st.consumers.map((c) => {
                const w = worktreeOf(c, worktrees);
                const name = c.branch || basename(c.workspace);
                return (
                  <tr key={c.workspace}>
                    <th scope="row">{w ? <a href={href.detail(project, w.id)}>{name}</a> : name}<div class="small muted mono">{basename(c.workspace)}</div></th>
                    <td>
                      <ul class="plain">
                        {c.services.map((s) => (
                          <li key={s} class="row">
                            <span class="mono">{s}</span>
                            <button type="button" class="btn sm quiet" disabled={!w || unlink.busy} onClick={() => w && void unlink.run(w, s)} aria-label={t.shared.unlinkNamed(s, name)}>{t.shared.unlink}</button>
                          </li>
                        ))}
                      </ul>
                    </td>
                    <td><Badge view={c.active ? { label: t.shared.active, icon: "running", tone: "ok" } : { label: t.shared.inactive, icon: "stopped", tone: "neutral" }} /></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      <LinkForm project={project} declared={declared} worktrees={worktrees} onChanged={onChanged} />
    </section>
  );
}

function LinkForm({ project, declared, worktrees, onChanged }: { project: string; declared: string[]; worktrees: Worktree[]; onChanged: () => void }) {
  const [wtId, setWtId] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [join, setJoin] = useState("");
  const list = useMemo(() => worktrees.filter((w) => w.branch || w.path), [worktrees]);
  useEffect(() => { if (!wtId && list[0]) setWtId(list[0].id); }, [list.length]);
  const link = useAction(async () => {
    const w = list.find((x) => x.id === wtId);
    if (!w || picked.length === 0) return;
    const joinList = join.split(/[\s,]+/).filter(Boolean);
    await api.put(`${wtPath(project, w.id)}/shared`, { use: picked, join: joinList });
    announce(t.shared.linked(w.branch || basename(w.path)));
    setPicked([]);
    setJoin("");
    onChanged();
  });
  if (declared.length === 0 || list.length === 0) return null;
  const toggle = (s: string) => setPicked(picked.includes(s) ? picked.filter((x) => x !== s) : [...picked, s]);
  return (
    <form onSubmit={(e) => { e.preventDefault(); void link.run(); }} class="stack" aria-labelledby="sh-link-title">
      <h3 id="sh-link-title">{t.shared.linkTitle}</h3>
      <Field id="sh-link-wt" label={t.shared.linkInstance}>
        <select id="sh-link-wt" class="input auto" value={wtId} onChange={(e) => setWtId((e.currentTarget as HTMLSelectElement).value)}>
          {list.map((w) => <option key={w.id} value={w.id}>{w.branch || basename(w.path)}</option>)}
        </select>
      </Field>
      <fieldset>
        <legend>{t.shared.linkServices}</legend>
        {declared.map((s, i) => (
          <div class="check field" key={s}>
            <input id={`sh-use-${i}`} type="checkbox" checked={picked.includes(s)} onChange={() => toggle(s)} />
            <label for={`sh-use-${i}`} class="mono">{s}</label>
          </div>
        ))}
      </fieldset>
      <Field id="sh-join" label={t.shared.linkJoin} hint={t.shared.linkJoinHint}>
        <input id="sh-join" class="input mono" value={join} autocomplete="off" spellcheck={false} aria-describedby="sh-join-hint" onInput={(e) => setJoin((e.currentTarget as HTMLInputElement).value)} />
      </Field>
      <ErrorBanner error={link.error} />
      <div><button type="submit" class="btn primary" disabled={link.busy || picked.length === 0}>{t.shared.linkButton}</button></div>
    </form>
  );
}

function AddService({ project, worktrees, onChanged, heading, existing }: { project: string; worktrees: Worktree[]; onChanged: () => void; heading: string; existing: string[] }) {
  const [name, setName] = useState("");
  const [from, setFrom] = useState("");
  const [publish, setPublish] = useState(false);
  const add = useAction(async () => {
    const services = name.split(/[\s,]+/).filter(Boolean);
    if (services.length === 0) return;
    const body: Record<string, unknown> = { services };
    if (from) body.from = from;
    if (publish) body.publish = true;
    await api.post(`${projectPath(project)}/shared`, body);
    announce(t.shared.added(services.join(", ")));
    setName("");
    onChanged();
  });
  const dup = existing.includes(name.trim());
  return (
    <section class="card" aria-labelledby="sh-add">
      <h2 id="sh-add">{heading}</h2>
      <form onSubmit={(e) => { e.preventDefault(); void add.run(); }} class="stack">
        <Field id="sh-add-name" label={t.shared.addService} hint={t.shared.addServiceHint}>
          <input id="sh-add-name" class="input mono" value={name} autocomplete="off" spellcheck={false} aria-describedby="sh-add-name-hint" onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} />
        </Field>
        {existing.length === 0 ? (
          <Field id="sh-add-from" label={t.shared.addFrom} hint={t.shared.addFromHint}>
            <select id="sh-add-from" class="input auto" value={from} aria-describedby="sh-add-from-hint" onChange={(e) => setFrom((e.currentTarget as HTMLSelectElement).value)}>
              <option value="">{worktrees.find((w) => w.main)?.branch ?? "main"}</option>
              {worktrees.filter((w) => !w.main).map((w) => <option key={w.id} value={w.branch || w.path}>{w.branch || basename(w.path)}</option>)}
            </select>
          </Field>
        ) : null}
        {existing.length === 0 ? (
          <div class="field">
            <div class="check">
              <input id="sh-add-publish" type="checkbox" checked={publish} aria-describedby="sh-add-publish-hint" onChange={(e) => setPublish((e.currentTarget as HTMLInputElement).checked)} />
              <label for="sh-add-publish">{t.shared.addPublish}</label>
            </div>
            <p class="hint" id="sh-add-publish-hint">{t.shared.addPublishHint}</p>
          </div>
        ) : null}
        <ErrorBanner error={add.error} />
        <div><button type="submit" class="btn primary" disabled={add.busy || !name.trim() || dup}>{t.shared.addButton}</button></div>
      </form>
    </section>
  );
}
