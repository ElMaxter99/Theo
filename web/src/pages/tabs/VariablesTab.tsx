import { useEffect, useState } from "preact/hooks";
import { api, wtPath } from "../../api/client.ts";
import type { EnvVar, WorktreeDetail } from "../../api/types.ts";
import { ErrorBanner, Field, Loading, describedBy } from "../../components/ui.tsx";
import { ConfirmDialog, Modal } from "../../components/Dialog.tsx";
import { Icon } from "../../components/Icon.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { parseList, validEnvName } from "../../lib/format.ts";
import { announce } from "../../lib/live.ts";
import { t } from "../../strings.ts";

const REVEAL_SECONDS = 30;

export function VariablesTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const base = `${wtPath(project, w.id)}/env`;
  const res = useResource<EnvVar[]>(() => api.get<EnvVar[]>(base), [project, w.id]);
  const [editing, setEditing] = useState<EnvVar | "new" | null>(null);
  const [deleting, setDeleting] = useState<EnvVar | null>(null);
  const [revealed, setRevealed] = useState<Record<string, { value: string; left: number }>>({});
  const reveal = useAction(async (name: string) => {
    const r = await api.post<{ value: string }>(`${base}/${encodeURIComponent(name)}/reveal`, {});
    setRevealed((m) => ({ ...m, [name]: { value: r.value, left: REVEAL_SECONDS } }));
    announce(t.vars.shownFor(name));
  });

  // Countdown: revealed values are dropped from memory after 30 seconds.
  useEffect(() => {
    if (Object.keys(revealed).length === 0) return;
    const id = window.setInterval(() => {
      setRevealed((m) => {
        const next: typeof m = {};
        for (const [k, v] of Object.entries(m)) if (v.left > 1) next[k] = { value: v.value, left: v.left - 1 };
        return next;
      });
    }, 1000);
    return () => clearInterval(id);
  }, [Object.keys(revealed).length > 0]);
  // Leaving the tab clears everything revealed.
  useEffect(() => () => setRevealed({}), []);

  const hide = (name: string) => { setRevealed((m) => { const n = { ...m }; delete n[name]; return n; }); announce(t.vars.hiddenMsg(name)); };

  return (
    <div class="stack">
      <div class="row between">
        <p class="muted">{t.vars.note}</p>
        <button type="button" class="btn primary" onClick={() => setEditing("new")}><Icon name="plus" /> {t.vars.add}</button>
      </div>
      {res.loading && !res.data ? <Loading /> : null}
      <ErrorBanner error={res.error ?? reveal.error} />
      {res.data && res.data.length === 0 ? <p class="empty">{t.vars.none}</p> : null}
      {res.data && res.data.length > 0 ? (
        <div class="table-wrap" tabIndex={0} role="region" aria-label={t.vars.title}>
          <table class="data">
            <caption class="sr-only">{t.vars.title}</caption>
            <thead>
              <tr>
                <th scope="col">{t.vars.colName}</th>
                <th scope="col">{t.vars.colValue}</th>
                <th scope="col">{t.vars.colScope}</th>
                <th scope="col">{t.vars.colActions}</th>
              </tr>
            </thead>
            <tbody>
              {res.data.map((v) => {
                const r = revealed[v.name];
                return (
                  <tr key={v.name}>
                    <th scope="row" class="mono">{v.name}</th>
                    <td class="mono">
                      {v.secret ? (
                        <>
                          <span>{r ? r.value : t.vars.masked}</span>{" "}
                          <span class="tag"><Icon name="lock" size={11} /> {t.vars.secret}</span>
                        </>
                      ) : v.value}
                    </td>
                    <td class="small">{v.services && v.services.length > 0 ? v.services.join(", ") : t.vars.allServices}</td>
                    <td class="actions">
                      <div class="row">
                        {v.secret ? (
                          r ? <button type="button" class="btn sm" onClick={() => hide(v.name)} aria-label={`${t.vars.hide} ${v.name}`}><Icon name="eye" /> {t.vars.hiding(r.left)}</button>
                            : <button type="button" class="btn sm" disabled={reveal.busy} onClick={() => void reveal.run(v.name)} aria-label={`${t.vars.show}: ${v.name}`}><Icon name="eye" /> {t.vars.show}</button>
                        ) : null}
                        <button type="button" class="btn sm" onClick={() => setEditing(v)} aria-label={`${t.vars.edit} ${v.name}`}>{t.vars.edit}</button>
                        <button type="button" class="btn sm danger" onClick={() => setDeleting(v)} aria-label={`${t.vars.delete} ${v.name}`}>{t.vars.delete}</button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      {editing ? <VarForm base={base} existing={editing === "new" ? null : editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); res.reload(); }} /> : null}
      {deleting ? (
        <ConfirmDialog title={t.vars.deleteTitle(deleting.name)} body={t.vars.deleteBody} confirmLabel={t.vars.delete} onClose={() => setDeleting(null)}
          onConfirm={async () => { await api.del(`${base}/${encodeURIComponent(deleting.name)}`); announce(t.vars.deleted(deleting.name)); res.reload(); }} />
      ) : null}
    </div>
  );
}

function VarForm({ base, existing, onClose, onSaved }: { base: string; existing: EnvVar | null; onClose: () => void; onSaved: () => void }) {
  const [name, setName] = useState(existing?.name ?? "");
  const [value, setValue] = useState(existing?.secret ? "" : (existing?.value ?? ""));
  const [secret, setSecret] = useState(!!existing?.secret);
  const [services, setServices] = useState((existing?.services ?? []).join(", "));
  const [nameErr, setNameErr] = useState("");
  const save = useAction(async () => {
    await api.put(`${base}/${encodeURIComponent(name)}`, { value, secret, services: parseList(services) });
    announce(t.vars.saved(name));
    onSaved();
  });
  const submit = (e: Event) => {
    e.preventDefault();
    if (!name.trim()) { setNameErr(t.vars.nameRequired); return; }
    if (!validEnvName(name)) { setNameErr(t.vars.nameInvalid); return; }
    setNameErr("");
    void save.run();
  };
  return (
    <Modal title={existing ? t.vars.formEdit(existing.name) : t.vars.formAdd} onClose={onClose}>
      <form onSubmit={submit} noValidate>
        <Field id="var-name" label={t.vars.name} hint={t.vars.nameHint} error={nameErr}>
          <input id="var-name" class="input mono" value={name} readOnly={!!existing} autoFocus={!existing} autocomplete="off" spellcheck={false}
            onInput={(e) => setName((e.currentTarget as HTMLInputElement).value)} {...describedBy("var-name", t.vars.nameHint, nameErr)} />
        </Field>
        <Field id="var-value" label={t.vars.value} hint={existing?.secret ? t.vars.valueEditSecretHint : undefined}>
          <input id="var-value" class="input mono" type={secret ? "password" : "text"} value={value} autocomplete="off" spellcheck={false}
            onInput={(e) => setValue((e.currentTarget as HTMLInputElement).value)} {...describedBy("var-value", existing?.secret ? t.vars.valueEditSecretHint : undefined)} />
        </Field>
        <div class="check field">
          <input id="var-secret" type="checkbox" checked={secret} onChange={(e) => setSecret((e.currentTarget as HTMLInputElement).checked)} />
          <label for="var-secret">{t.vars.isSecret}</label>
        </div>
        <Field id="var-services" label={t.vars.services} hint={t.vars.servicesHint}>
          <input id="var-services" class="input mono" value={services} autocomplete="off" spellcheck={false}
            onInput={(e) => setServices((e.currentTarget as HTMLInputElement).value)} {...describedBy("var-services", t.vars.servicesHint)} />
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
