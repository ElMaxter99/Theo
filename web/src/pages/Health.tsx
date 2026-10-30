// Reconciliation and health (view-phase 2.6): the differences between what Theo recorded and what exists, grouped by
// kind, with what to do about each. Only Theo's own records can be forgotten; nothing else is ever touched.
import { useEffect, useMemo, useState } from "preact/hooks";
import { api, ApiError } from "../api/client.ts";
import type { Finding } from "../api/types.ts";
import { Banner, ErrorBanner, Loading, PageHead } from "../components/ui.tsx";
import { ConfirmDialog } from "../components/Dialog.tsx";
import { Icon } from "../components/Icon.tsx";
import { findingId, groupFindings, refreshHealth, useHealth } from "../lib/health.ts";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function Health() {
  const h = useHealth();
  const [project, setProject] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [confirming, setConfirming] = useState(false);
  const [forgetError, setForgetError] = useState<ApiError | undefined>();
  useEffect(() => setTitle([t.reconcile.title]), []);
  useEffect(() => { void refreshHealth(); }, []);

  const all = h.report?.findings ?? [];
  const projects = useMemo(() => [...new Set(all.map((f) => f.project).filter((p): p is string => !!p))].sort(), [all]);
  const shown = project ? all.filter((f) => f.project === project) : all;
  const groups = groupFindings(shown);
  // A record that disappeared (after "forget" or a refresh) can no longer be selected.
  const ids = new Set(all.map(findingId));
  const selected = [...picked].filter((id) => ids.has(id));

  const toggle = (id: string) => {
    const n = new Set(picked);
    if (n.has(id)) n.delete(id); else n.add(id);
    setPicked(n);
  };
  const toggleGroup = (items: Finding[], on: boolean) => {
    const n = new Set(picked);
    for (const f of items) if (f.forgettable) { if (on) n.add(findingId(f)); else n.delete(findingId(f)); }
    setPicked(n);
  };

  const forget = async () => {
    setForgetError(undefined);
    try {
      await api.post("/reconcile/forget", { finding_ids: selected });
      announce(t.reconcile.forgotten(selected.length));
      setPicked(new Set());
      await refreshHealth();
    } catch (e) {
      setForgetError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
      throw e;
    }
  };

  return (
    <>
      <PageHead
        title={t.reconcile.title}
        sub={h.report ? t.reconcile.summary(all.length) : undefined}
        actions={<button type="button" class="btn" disabled={h.loading} onClick={() => void refreshHealth()}><Icon name="restart" /> {h.loading ? t.reconcile.checking : t.reconcile.refresh}</button>}
      />
      <p>{t.reconcile.intro}</p>
      <p><strong>{t.reconcile.fixedNote}</strong></p>
      {h.loading && !h.report ? <Loading label={t.reconcile.checking} /> : null}
      {h.error && !h.report ? <ErrorBanner error={h.error} title={t.reconcile.loadError} /> : null}
      {h.report?.docker_error ? (
        <Banner tone="warn" title={t.reconcile.dockerTitle} role="status"><p>{h.report.docker_error}</p><p>{t.reconcile.dockerBody}</p></Banner>
      ) : null}
      {h.report && all.length === 0 && !h.report.docker_error ? <Banner tone="ok"><p>{t.reconcile.none}</p></Banner> : null}
      {projects.length > 1 ? (
        <div class="field">
          <label for="hl-project">{t.reconcile.filterLabel}</label>
          <select id="hl-project" class="input auto" value={project} onChange={(e) => setProject((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">{t.reconcile.allProjects}</option>
            {projects.map((p) => <option key={p} value={p}>{p}</option>)}
          </select>
        </div>
      ) : null}
      {groups.map((g) => {
        const forgettable = g.items.filter((f) => f.forgettable);
        const allOn = forgettable.length > 0 && forgettable.every((f) => picked.has(findingId(f)));
        const label = t.reconcile.kinds[g.kind] ?? g.kind;
        const gid = "hl-g-" + g.kind.replace(/[^a-z0-9]+/gi, "-");
        return (
          <section class="card stack" key={g.kind} aria-labelledby={gid}>
            <h2 id={gid}>{t.reconcile.group(label, g.items.length)}</h2>
            {forgettable.length > 1 ? (
              <div class="check">
                <input id={gid + "-all"} type="checkbox" checked={allOn} onChange={(e) => toggleGroup(g.items, (e.currentTarget as HTMLInputElement).checked)} />
                <label for={gid + "-all"}>{t.reconcile.selectAll}</label>
              </div>
            ) : null}
            <div class="table-wrap" tabIndex={0} role="region" aria-label={label}>
              <table class="data">
                <caption class="sr-only">{label}</caption>
                <thead>
                  <tr>
                    <th scope="col">{t.reconcile.colSubject}</th>
                    <th scope="col">{t.reconcile.colDetail}</th>
                    <th scope="col">{t.reconcile.colFix}</th>
                    <th scope="col">{t.reconcile.colForget}</th>
                  </tr>
                </thead>
                <tbody>
                  {g.items.map((f) => {
                    const id = findingId(f);
                    return (
                      <tr key={id}>
                        <th scope="row">
                          <span class="mono">{f.subject}</span>
                          {f.project ? <div class="small muted">{f.project}</div> : null}
                        </th>
                        <td>{f.detail}</td>
                        <td>{f.fix ?? "—"}</td>
                        <td>
                          {f.forgettable ? (
                            <div class="check">
                              <input id={"hl-f-" + id} type="checkbox" checked={picked.has(id)} onChange={() => toggle(id)} />
                              <label for={"hl-f-" + id}><span class="sr-only">{t.reconcile.forgetSelect(f.subject)}</span><span aria-hidden="true">{t.reconcile.forgettable}</span></label>
                            </div>
                          ) : <span class="muted">{t.reconcile.notForgettable}</span>}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </section>
        );
      })}
      {all.some((f) => f.forgettable) ? (
        <div class="row">
          <button type="button" class="btn" disabled={selected.length === 0} aria-describedby="hl-forget-hint" onClick={() => setConfirming(true)}>{t.reconcile.forgetButton}{selected.length > 0 ? ` (${selected.length})` : ""}</button>
          <span class="hint" id="hl-forget-hint">{selected.length === 0 ? t.reconcile.forgetNone : t.reconcile.fixedNote}</span>
        </div>
      ) : null}
      <ErrorBanner error={forgetError} />
      {confirming ? (
        <ConfirmDialog
          title={t.reconcile.forgetTitle(selected.length)}
          body={t.reconcile.forgetBody}
          confirmLabel={t.reconcile.forgetConfirm}
          onClose={() => setConfirming(false)}
          onConfirm={forget}
        />
      ) : null}
    </>
  );
}
