// V-08 Cleanup, inactive worktrees: how many there are at a glance (counters that are also filters), a table to pick
// some or all, and two actions on the pick: remove the folder (park, the branch stays) or remove the folder and the
// local branch (only the ones whose commits are all on their remote; the server checks again). Nothing is removed
// without typing the word. The plan comes from GET /projects/{p}/park-inactive, the action is its POST.
import { useEffect, useState } from "preact/hooks";
import { api, projectPath, qs } from "../api/client.ts";
import type { Operation, ParkInactiveEntry, ParkInactiveReport } from "../api/types.ts";
import { sizeText } from "../lib/lifecycle.ts";
import { useResource } from "../lib/hooks.ts";
import { showParkRun } from "../lib/parkRun.ts";
import { parkReason } from "./ParkProgress.tsx";
import { t } from "../strings.ts";
import { TypedConfirmDialog } from "./Dialog.tsx";
import { CopyCommand, theoCmd } from "./CopyCommand.tsx";
import { ErrorBanner, Loading } from "./ui.tsx";

type Filter = "all" | "safe" | "changes" | "unpushed" | "gone" | "blocked";
// Most uncommitted files saved in one WIP commit (the server default is 50).
const MAX_SAVED = 1000;
const FILTERS: Filter[] = ["all", "safe", "changes", "unpushed", "gone", "blocked"];

const matches = (e: ParkInactiveEntry, f: Filter): boolean => {
  switch (f) {
    case "safe": return !!e.safe;
    case "changes": return e.changes > 0;
    case "unpushed": return e.unpushed > 0;
    case "gone": return !!e.upstream_gone;
    case "blocked": return !!e.blocked;
    default: return true;
  }
};

export function InactivePicker({ project, onChanged, onKeep }: { project: string; onChanged: () => void; onKeep: (path: string) => void }) {
  const c = t.cleanup;
  // Saving changes in a local WIP commit and keeping a detached HEAD in a branch lose nothing, so they start on;
  // deleting ignored files that cannot be rebuilt (.env, certs...) does not, so it starts off.
  const [saveChanges, setSaveChanges] = useState(true);
  const [rescue, setRescue] = useState(true);
  const [withIgnored, setWithIgnored] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [mode, setMode] = useState<"park" | "delete" | null>(null);
  const plan = useResource<ParkInactiveReport>(
    () => api.get<ParkInactiveReport>(`${projectPath(project)}/park-inactive${qs({ save_changes: saveChanges, include_ignored: withIgnored, rescue_detached: rescue, max_changes: saveChanges ? MAX_SAVED : undefined, size: true })}`),
    [project, saveChanges, withIgnored, rescue],
    { project },
  );
  const entries = plan.data?.entries ?? [];
  const summary = plan.data?.summary;
  const pickable = (e: ParkInactiveEntry) => !e.reason;
  // What was picked and is no longer offered (it changed, or it was parked) is dropped.
  useEffect(() => {
    setPicked((cur) => {
      const ok = new Set(entries.filter(pickable).map((e) => e.path));
      const next = new Set([...cur].filter((p) => ok.has(p)));
      return next.size === cur.size ? cur : next;
    });
  }, [plan.data]);

  const shown = entries.filter((e) => matches(e, filter));
  const shownPickable = shown.filter(pickable);
  const allShown = shownPickable.length > 0 && shownPickable.every((e) => picked.has(e.path));
  const chosen = entries.filter((e) => picked.has(e.path));
  const deletable = chosen.filter((e) => e.branch_deletable);
  const bytes = chosen.reduce((n, e) => n + (e.size_bytes ?? 0), 0);
  const count = (f: Filter): number => f === "all" ? summary?.total ?? 0 : f === "safe" ? summary?.safe ?? 0 : f === "changes" ? summary?.with_changes ?? 0
    : f === "unpushed" ? summary?.with_unpushed ?? 0 : f === "gone" ? summary?.upstream_gone ?? 0 : summary?.blocked ?? 0;
  const toggle = (path: string) => setPicked((cur) => { const n = new Set(cur); if (n.has(path)) n.delete(path); else n.add(path); return n; });
  const setMany = (list: ParkInactiveEntry[], on: boolean) => setPicked((cur) => { const n = new Set(cur); for (const e of list) { if (on) n.add(e.path); else n.delete(e.path); } return n; });
  const flag = (f: Filter) => t.cleanup[`pickChip${f[0]!.toUpperCase()}${f.slice(1)}` as "pickChipAll"];
  const name = (e: ParkInactiveEntry) => e.branch ?? e.path;
  const why = parkReason;
  const cmd = theoCmd("worktree", "park-inactive", project, saveChanges && "--save-changes", saveChanges && "--max-changes", saveChanges && String(MAX_SAVED), withIgnored && "--include-ignored", rescue && "--rescue-detached",
    mode === "delete" && "--delete-branches", ...chosen.flatMap((e) => ["--only", name(e)]), "--confirm", project);

  if (plan.loading && !plan.data) return <Loading />;
  if (plan.error && !plan.data) return <ErrorBanner error={plan.error} />;
  if (!plan.data || entries.length === 0) return null;
  return (
    <div class="stack-sm">
      <p class="small muted">{c.pickLead}</p>
      <div class="chips" role="group" aria-label={c.pickFilter}>
        {FILTERS.filter((f) => f === "all" || count(f) > 0).map((f) => (
          <button key={f} type="button" class="chip" aria-pressed={filter === f} onClick={() => setFilter(f)}>{flag(f)} <span class="chip-n">{count(f)}</span></button>
        ))}
      </div>
      <p class="small muted">{filter === "safe" ? c.pickHintSafe : filter === "blocked" ? c.pickHintBlocked : filter === "gone" ? c.pickHintGone : c.pickHintAll(summary?.parkable ?? 0, sizeText(summary?.size_bytes))}</p>
      <fieldset class="stack-sm">
        <label class="row"><input type="checkbox" checked={saveChanges} onChange={(e) => setSaveChanges((e.currentTarget as HTMLInputElement).checked)} /> {c.parkAllSave}</label>
        <label class="row"><input type="checkbox" checked={rescue} onChange={(e) => setRescue((e.currentTarget as HTMLInputElement).checked)} /> {c.pickRescue}</label>
        <label class="row"><input type="checkbox" checked={withIgnored} onChange={(e) => setWithIgnored((e.currentTarget as HTMLInputElement).checked)} /> {c.parkAllIgnored}</label>
      </fieldset>
      <div class="row">
        <button type="button" class="btn sm" disabled={(summary?.safe ?? 0) === 0} onClick={() => setMany(entries.filter((e) => e.safe && pickable(e)), true)}>{c.pickSelectSafe}</button>
      </div>
      {chosen.length > 0 ? (
        <div class="bulkbar" role="region" aria-label={c.pickSelected(chosen.length)}>
          <strong>{c.pickSelected(chosen.length)} · {sizeText(bytes)}</strong>
          <button type="button" class="btn sm danger" onClick={() => setMode("park")}>{c.pickPark}</button>
          <button type="button" class="btn sm danger" disabled={deletable.length === 0} title={deletable.length === 0 ? c.pickDeleteDisabled : undefined} onClick={() => setMode("delete")}>{c.pickDelete(deletable.length)}</button>
          <button type="button" class="btn sm quiet" onClick={() => setPicked(new Set())}>{c.pickClear}</button>
          <CopyCommand command={cmd} />
        </div>
      ) : null}
      {shownPickable.length === 0 && shown.length > 0 ? <p class="small">{c.pickNonePickable}</p> : null}
      {shown.length === 0 ? <p class="empty">{c.pickEmpty}</p> : (
        <div class="table-wrap" tabIndex={0} role="region" aria-label={c.inactiveTitle(shown.length)}>
          <table class="data rows">
            <caption class="sr-only">{c.inactiveTitle(shown.length)}</caption>
            <thead>
              <tr>
                <th scope="col" class="pick"><input type="checkbox" aria-label={c.pickSelectAll} checked={allShown} disabled={shownPickable.length === 0} onChange={() => setMany(shownPickable, !allShown)} /></th>
                <th scope="col">{t.panel.colWorktree}</th>
                <th scope="col" class="num">{c.pickColIdle}</th>
                <th scope="col" class="num">{c.pickColChanges}</th>
                <th scope="col" class="num">{c.pickColUnpushed}</th>
                <th scope="col">{c.pickColRemote}</th>
                <th scope="col" class="num">{c.colSize}</th>
                <th scope="col"><span class="sr-only">{t.life.keep}</span></th>
              </tr>
            </thead>
            <tbody>
              {shown.map((e) => (
                <tr key={e.path} aria-selected={picked.has(e.path)}>
                  <td class="pick"><input type="checkbox" disabled={!pickable(e)} checked={picked.has(e.path)} aria-label={c.pickSelect(name(e))} onChange={() => toggle(e.path)} /></td>
                  <th scope="row">{name(e)}{e.reason ? <div class="small muted">{why(e)}</div> : e.rescue_branch ? <div class="small muted">{c.pickRescueInfo(e.rescue_branch)}</div> : null}</th>
                  <td class="num">{c.pickDays(e.idle_days)}</td>
                  <td class="num">{e.changes > 0 ? e.changes : <span class="muted">—</span>}</td>
                  <td class="num">{e.unpushed > 0 ? e.unpushed : <span class="muted">—</span>}</td>
                  <td>{e.upstream_gone ? c.pickRemoteGone : e.safe ? c.pickRemoteOk : <span class="muted">—</span>}</td>
                  <td class="num">{sizeText(e.size_bytes)}</td>
                  <td>{e.blocked ? null : <button type="button" class="btn sm" onClick={() => onKeep(e.path)}>{t.life.keep}</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {mode ? (
        <TypedConfirmDialog
          title={mode === "delete" ? c.pickDelete(deletable.length) : c.pickPark}
          lead={<>{c.pickTyped(c.word, chosen.length)}{mode === "delete" ? <> {c.pickDeleteLead(deletable.length, chosen.length - deletable.length)}</> : null}</>}
          expected={c.word}
          affectedTitle={c.pickAffected(chosen.length, sizeText(bytes))}
          affected={chosen.map((e) => ({ name: name(e), detail: `${c.pickDays(e.idle_days)}${e.changes > 0 ? ` · ${c.changes(e.changes)}` : ""} · ${mode === "delete" && e.branch_deletable ? c.pickBranchGoes : c.pickBranchStays}` }))}
          preserved={mode === "delete" ? [c.pickKeepRemote, c.keepVolumes] : [c.parkAllKeep]}
          confirmLabel={mode === "delete" ? c.pickDelete(deletable.length) : c.pickPark}
          onClose={() => setMode(null)}
          onConfirm={async () => {
            const op = await api.post<Operation>(`${projectPath(project)}/park-inactive`, {
              save_changes: saveChanges, include_ignored: withIgnored, rescue_detached: rescue, max_changes: saveChanges ? MAX_SAVED : undefined, delete_branches: mode === "delete",
              paths: chosen.map((e) => e.path), confirm: project,
            });
            showParkRun({ project, opId: op.id }); // the progress window is shown by the page, which outlives this list
            setPicked(new Set());
          }}
        />
      ) : null}
    </div>
  );
}
