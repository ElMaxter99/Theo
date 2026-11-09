// V-08 Cleanup, redesigned (board 7 to 13 of the design canvas). One state per worktree (ready, waiting for your OK,
// blocked, kept), grouped by what the person has to decide, in four steps: Review, Confirm, Removing, Result.
// Everything that is removed goes through the park-inactive API (the folder goes, the branch and the volumes stay and
// can be resumed); only worktrees whose folder is already gone go through the clean API, which forgets them.
import { useEffect, useState } from "preact/hooks";
import { api, ApiError, projectPath, qs } from "../api/client.ts";
import type { CleanEntry, CleanReport, LifeReport, LifeWorktree, Operation, ParkInactiveEntry, ParkInactiveReport } from "../api/types.ts";
import { KeepDialog } from "../components/LifecycleDialogs.tsx";
import { Modal } from "../components/Dialog.tsx";
import { ParkRunWindow, parkReason } from "../components/ParkProgress.tsx";
import { CopyCommand, theoCmd } from "../components/CopyCommand.tsx";
import { Banner, ErrorBanner, Loading } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { sizeText } from "../lib/lifecycle.ts";
import { useResource } from "../lib/hooks.ts";
import { announce } from "../lib/live.ts";
import { setParkResult, showParkRun, useParkResult, useParkRun } from "../lib/parkRun.ts";
import type { ParkRunRef } from "../lib/parkRun.ts";
import { refreshStatus, useStatus } from "../lib/statusStore.ts";
import { t } from "../strings.ts";
import { ParkResultPanel } from "./CleanupResult.tsx";

type Kind = "ready" | "missing" | "ask-ignored" | "ask-dirty" | "blocked" | "kept";
type State = "ready" | "ask" | "blocked" | "kept";
type Decision = "apply" | "each" | "skip";
type Group = "ignored" | "dirty";

/** Most uncommitted files saved in one work-in-progress commit. */
const MAX_SAVED = 1000;

export interface Row {
  key: string;
  project: string;
  path: string;
  name: string;
  kind: Kind;
  why: string;
  idle: number;
  changes: number;
  ignored: string[];
  ignoredN: number;
  size?: number;
  branchDeletable: boolean;
  unblock?: { text: string; cmd?: string };
  note?: string;
  w?: LifeWorktree;
}

interface Plan { project: string; clean: CleanReport; life: LifeReport | null; park: ParkInactiveReport | null; base: string }

const ORDER: Kind[] = ["ready", "missing", "ask-ignored", "ask-dirty", "blocked", "kept"];

function unblockFor(e: ParkInactiveEntry, path: string): Row["unblock"] {
  const c = t.cleanFlow;
  switch (e.reason_code) {
    case "locked": return { text: c.unblockLocked, cmd: `git worktree unlock ${path}` };
    case "agent": return { text: c.unblockAgent };
    case "clone": return { text: c.unblockClone };
    case "no-branch": return { text: c.unblockNoBranch };
    case "too-many-changes": return { text: c.unblockTooMany(e.changes) };
    default: return { text: c.unblockOther };
  }
}

function rowsOf(p: Plan): Row[] {
  const c = t.cleanFlow;
  const life = (path: string) => p.life?.worktrees.find((w) => w.path === path);
  const parkBy = new Map((p.park?.entries ?? []).map((e) => [e.path, e]));
  const name = (e: { branch?: string; path: string }) => e.branch ?? e.path;
  const out: Row[] = [];
  const base: Omit<Row, "key" | "path" | "name" | "kind" | "why" | "idle"> = { project: p.project, changes: 0, ignored: [], ignoredN: 0, branchDeletable: false };
  const mergedWhy = (e: CleanEntry) => e.merged_by === "content" ? c.mergedSquash : c.mergedInto(p.base || "main");
  const fromPark = (e: CleanEntry, why: string, wasMerged: boolean): Row => {
    const pe = parkBy.get(e.path);
    const common = { ...base, key: `${p.project}|${e.path}`, path: e.path, name: name(e), idle: e.idle_days, why, w: life(e.path), changes: pe?.changes ?? e.changes ?? 0 };
    if (!pe) return { ...common, kind: "blocked", unblock: { text: c.unblockOther } };
    const size = pe.size_bytes;
    if (pe.reason) return { ...common, kind: "blocked", why: parkReason(pe), size, unblock: unblockFor(pe, e.path) };
    const ext = { ...common, size, ignored: pe.ignored_names ?? [], ignoredN: pe.ignored ?? 0, branchDeletable: !!pe.branch_deletable };
    if ((pe.ignored ?? 0) > 0) return { ...ext, kind: "ask-ignored" };
    if (wasMerged && ext.changes > 0) return { ...ext, kind: "ask-dirty" };
    return { ...ext, kind: "ready" };
  };
  for (const e of p.clean.close) {
    if (e.state === "missing" && !e.reason) {
      out.push({ ...base, key: `${p.project}|${e.path}`, path: e.path, name: name(e), kind: "missing", why: c.folderGone, idle: e.idle_days, branchDeletable: false });
    } else if (e.reason && !(e.state === "merged" && (e.changes ?? 0) > 0 && /uncommitted/.test(e.reason))) {
      out.push({ ...base, key: `${p.project}|${e.path}`, path: e.path, name: name(e), kind: "blocked", why: e.reason, idle: e.idle_days, w: life(e.path),
        unblock: { text: /in use|running|instance/.test(e.reason) ? c.unblockInUse : /agent/.test(e.reason) ? c.unblockAgent : /locked/.test(e.reason) ? c.unblockLocked : c.unblockOther, cmd: /locked/.test(e.reason) ? `git worktree unlock ${e.path}` : undefined } });
    } else {
      out.push(fromPark(e, mergedWhy(e), true));
    }
  }
  for (const e of p.clean.inactive) out.push(fromPark(e, e.upstream_gone ? c.remoteGone : "", false));
  for (const e of p.clean.kept) out.push({ ...base, key: `${p.project}|${e.path}`, path: e.path, name: name(e), kind: "kept", why: e.note ? `«${e.note}»` : "", idle: e.idle_days, w: life(e.path) });
  return out;
}

export function Cleanup({ initial }: { initial?: string }) {
  const status = useStatus();
  const names = (status.report?.projects ?? []).map((p) => p.name);
  const [project, setProject] = useState(initial ?? "");
  const chosen = project && names.includes(project) ? [project] : names;
  const key = chosen.join("|");
  const c = t.cleanFlow;
  const plans = useResource<Plan[]>(async () => Promise.all(chosen.map(async (p) => {
    const [clean, life] = await Promise.all([
      api.get<CleanReport>(`${projectPath(p)}/clean`),
      api.get<LifeReport>(`${projectPath(p)}/lifecycle${qs({ size: true })}`).catch(() => null),
    ]);
    // The park plan, with every option on, tells what each worktree would save and delete. Merged worktrees with
    // uncommitted files are named so they are part of it.
    const paths = [...clean.inactive.map((e) => e.path), ...clean.close.filter((e) => e.state === "merged").map((e) => e.path)];
    const park = paths.length === 0 ? null : await api.get<ParkInactiveReport>(`${projectPath(p)}/park-inactive${qs({ save_changes: true, include_ignored: true, rescue_detached: true, max_changes: MAX_SAVED, size: true, path: paths })}`).catch(() => null);
    const base = life?.worktrees.find((w) => w.base)?.base ?? "";
    return { project: p, clean, life, park, base };
  })), [key], { project: chosen.length === 1 ? chosen[0] : "*" });

  const [dec, setDec] = useState<Partial<Record<Group, Decision>>>({});
  const [off, setOff] = useState<Set<string>>(new Set());
  const [on, setOn] = useState<Set<string>>(new Set());
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState<State | "all">("all");
  const [confirming, setConfirming] = useState(false);
  const [keeping, setKeeping] = useState<Row | null>(null);
  const [actionError, setActionError] = useState<ApiError | undefined>();
  const run = useParkRun();
  const result = useParkResult();
  const data = plans.data ?? [];
  const rows = data.flatMap(rowsOf);
  const reload = () => { plans.reload(); void refreshStatus(); };
  const groupOf = (r: Row): Group | undefined => r.kind === "ask-ignored" ? "ignored" : r.kind === "ask-dirty" ? "dirty" : undefined;
  const stateOf = (r: Row): State => {
    if (r.kind === "blocked") return "blocked";
    if (r.kind === "kept") return "kept";
    const g = groupOf(r);
    if (!g) return "ready";
    const d = dec[g];
    return d === "apply" || (d === "each" && on.has(r.key)) ? "ready" : "ask";
  };
  const selected = (r: Row): boolean => {
    const s = stateOf(r);
    if (s !== "ready") return false;
    const g = groupOf(r);
    return g && dec[g] === "each" ? on.has(r.key) : !off.has(r.key);
  };
  const count = (s: State) => rows.filter((r) => stateOf(r) === s).length;
  const sel = rows.filter(selected);
  const selBytes = sel.reduce((n, r) => n + (r.size ?? 0), 0);
  const selSaves = sel.reduce((n, r) => n + (r.kind === "missing" ? 0 : r.changes), 0);
  const selIgnored = sel.filter((r) => r.ignoredN > 0);
  const [delBranches, setDelBranches] = useState(false);

  const toggle = (set: Set<string>, setter: (s: Set<string>) => void, k: string) => { const n = new Set(set); if (n.has(k)) n.delete(k); else n.add(k); setter(n); };
  const toggleSel = (r: Row) => {
    const g = groupOf(r);
    if (g && dec[g] === "each") toggle(on, setOn, r.key); else toggle(off, setOff, r.key);
  };
  const setAll = (list: Row[], value: boolean) => {
    const g = list[0] ? groupOf(list[0]) : undefined;
    if (g && dec[g] === "each") { const n = new Set(on); for (const r of list) { if (value) n.add(r.key); else n.delete(r.key); } setOn(n); }
    else { const n = new Set(off); for (const r of list) { if (value) n.delete(r.key); else n.add(r.key); } setOff(n); }
  };
  const decide = (g: Group, d: Decision) => setDec((cur) => ({ ...cur, [g]: d }));

  const step = result ? 3 : run ? 2 : confirming ? 1 : 0;
  const shown = (kind: Kind) => rows.filter((r) => r.kind === kind && (filter === "all" || stateOf(r) === filter));
  const waiting = count("ask");

  useEffect(() => { if (!run && result) reload(); }, [!!run, !!result]);

  return (
    <section class="stack" aria-labelledby="cl-title">
      <ParkRunWindow onClosed={reload} />
      <h2 id="cl-title">{c.heading}</h2>
      <p>{c.lead}</p>
      <ol class="cl-steps" aria-label={c.stepsLabel}>
        {c.steps.map((s, i) => <li key={s} aria-current={i === step ? "step" : undefined} class={i === step ? "on" : i < step ? "done" : ""}><span class="cl-step-n">{i + 1}</span>{s}</li>)}
      </ol>
      <div class="row controls">
        <div class="field">
          <label for="cl-project">{c.project}</label>
          <select id="cl-project" class="input auto" value={project} onChange={(e) => { setProject((e.currentTarget as HTMLSelectElement).value); setDec({}); setOff(new Set()); setOn(new Set()); }}>
            <option value="">{c.all}</option>
            {names.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </div>
        <button type="button" class="btn sm" onClick={reload}>{c.check}</button>
        {chosen.length === 1 ? <Threshold project={chosen[0]!} days={data[0]?.clean.inactive_after_days ?? 7} onSaved={reload} /> : null}
      </div>
      {plans.loading && !plans.data ? <Loading /> : null}
      <ErrorBanner error={plans.error ?? actionError} />
      {result ? <ParkResultPanel result={result} waiting={waiting} onDone={() => { setParkResult(null); reload(); }} onRetry={(ref) => { showParkRun(ref); }} /> : null}
      {plans.data && !result ? (
        <>
          {rows.length === 0 ? <Banner tone="ok" title={c.nothing}><p>{c.nothingSub}</p></Banner> : (
            <>
              <div class="cl-head" role="status">
                <h3>{c.headline(count("ready"), count("ask"), count("blocked"))}</h3>
                {count("ask") > 0 ? <p class="small muted">{c.headlineSub}</p> : null}
              </div>
              <div class="cl-tiles" role="group" aria-label={c.filter}>
                {([["ready", c.tileReady, "ok"], ["ask", c.tileAsk, "warn"], ["blocked", c.tileBlocked, "lock"], ["kept", c.tileKept, "info"]] as const).map(([s, label, icon]) => (
                  <button key={s} type="button" class={`cl-tile cl-${s}`} aria-pressed={filter === s} onClick={() => setFilter(filter === s ? "all" : s)}>
                    <span class="cl-tile-l"><Icon name={icon} size={14} /> {label}</span>
                    <strong>{count(s)}</strong>
                  </button>
                ))}
              </div>
              {ORDER.map((kind) => {
                const list = shown(kind);
                if (list.length === 0) return null;
                return <GroupCard key={kind} kind={kind} list={list} all={rows.filter((r) => r.kind === kind)} dec={dec} open={open} selected={selected} stateOf={stateOf}
                  onDecide={decide} onToggle={toggleSel} onAll={setAll} onOpen={(k) => toggle(open, setOpen, k)} onKeep={setKeeping} />;
              })}
              <Leftovers data={data} onChanged={reload} onError={setActionError} />
            </>
          )}
          <div class="cl-bar" role="region" aria-label={c.removeN(sel.length)}>
            {sel.length > 0 ? (
              <div class="grow">
                <strong>{c.barSelected(sel.length, sizeText(selBytes))}</strong>
                <div class="small">{selSaves > 0 ? c.barSaves(selSaves) : ""}{" "}{selIgnored.length > 0 ? <strong class="cl-warn">{c.barDeletes(ignoredSummary(selIgnored))}</strong> : null}</div>
              </div>
            ) : <div class="grow"><strong>{c.barNone}</strong> <span class="muted">{c.barNoneHint}</span></div>}
            <button type="button" class="btn danger" disabled={sel.length === 0} onClick={() => setConfirming(true)}><Icon name="trash" /> {c.removeN(sel.length)}</button>
          </div>
        </>
      ) : null}
      {confirming ? (
        <ConfirmRemove rows={sel} bytes={selBytes} saves={selSaves} delBranches={delBranches} onDelBranches={setDelBranches} onClose={() => setConfirming(false)}
          onDone={() => { setConfirming(false); setOff(new Set()); setOn(new Set()); reload(); }} onError={setActionError} />
      ) : null}
      {keeping?.w ? <KeepDialog project={keeping.project} w={keeping.w} onClose={() => setKeeping(null)} onDone={reload} /> : null}
    </section>
  );
}

/** "certs/ ×3, .claude/" from the ignored folders of the worktrees. */
function ignoredSummary(rows: Row[]): string {
  const m = new Map<string, number>();
  for (const r of rows) for (const n of r.ignored) m.set(n, (m.get(n) ?? 0) + 1);
  return [...m].map(([n, k]) => (k > 1 ? `${n} ×${k}` : n)).join(", ");
}

interface GroupProps {
  kind: Kind; list: Row[]; all: Row[]; dec: Partial<Record<Group, Decision>>; open: Set<string>;
  selected: (r: Row) => boolean; stateOf: (r: Row) => State;
  onDecide: (g: Group, d: Decision) => void; onToggle: (r: Row) => void; onAll: (list: Row[], on: boolean) => void; onOpen: (k: string) => void; onKeep: (r: Row) => void;
}

function GroupCard(p: GroupProps) {
  const c = t.cleanFlow;
  const { kind, list } = p;
  const g: Group | undefined = kind === "ask-ignored" ? "ignored" : kind === "ask-dirty" ? "dirty" : undefined;
  const title = { ready: c.gReady, missing: c.gMissing, "ask-ignored": c.gIgnored, "ask-dirty": c.gDirty, blocked: c.gBlocked, kept: c.gKept }[kind];
  const hint = { ready: c.gReadyHint, missing: c.gMissingHint, "ask-ignored": c.gIgnoredHint, "ask-dirty": c.gDirtyHint, blocked: c.gBlockedHint, kept: c.gKeptHint }[kind];
  const tone = g ? "ask" : kind === "blocked" ? "blocked" : kind === "kept" ? "kept" : "ready";
  const d = g ? p.dec[g] : undefined;
  const size = p.all.reduce((n, r) => n + (r.size ?? 0), 0);
  const selectable = kind === "ready" || kind === "missing" || !!g;
  const pickable = list.filter((r) => p.stateOf(r) === "ready");
  const allOn = pickable.length > 0 && pickable.every(p.selected);
  const ignoredNames = ignoredSummary(p.all);
  const groupId = `cl-g-${kind}`;
  return (
    <section class={`cl-group cl-g-${tone}`} aria-labelledby={groupId}>
      <header>
        <h3 id={groupId}>{title}</h3>
        <span class="small muted">{c.inGroup(p.all.length, sizeText(size))}</span>
        <p class="small grow-line">{hint}</p>
      </header>
      {g ? (
        <fieldset class="cl-decide">
          <legend>{c.decide(p.all.length)}</legend>
          <label class="radio-card"><input type="radio" name={`dec-${g}`} checked={d === "apply"} onChange={() => p.onDecide(g, "apply")} />
            <span><strong>{g === "ignored" ? c.optIgnoredApply(ignoredNames) : c.optDirtyApply}</strong>{g === "ignored" ? <span class="cl-tag"> {c.irreversible}</span> : null}<br /><span class="small muted">{g === "ignored" ? c.optIgnoredApplySub : c.optDirtyApplySub}</span></span></label>
          <label class="radio-card"><input type="radio" name={`dec-${g}`} checked={d === "each"} onChange={() => p.onDecide(g, "each")} />
            <span><strong>{c.optEach}</strong><br /><span class="small muted">{c.optEachSub}</span></span></label>
          <label class="radio-card"><input type="radio" name={`dec-${g}`} checked={d === "skip"} onChange={() => p.onDecide(g, "skip")} />
            <span><strong>{c.optSkip}</strong><br /><span class="small muted">{c.optSkipSub}</span></span></label>
        </fieldset>
      ) : null}
      <div class="table-wrap" tabIndex={0} role="region" aria-label={title}>
        <table class="data rows">
          <caption class="sr-only">{title}</caption>
          <thead>
            <tr>
              {selectable ? <th scope="col" class="pick"><input type="checkbox" aria-label={c.selectAll} checked={allOn} disabled={pickable.length === 0} onChange={() => p.onAll(pickable, !allOn)} /></th> : null}
              <th scope="col">{c.colWorktree}</th>
              <th scope="col">{kind === "blocked" || kind === "kept" ? c.colWhy : c.colIdle}</th>
              {selectable && kind !== "missing" ? <><th scope="col">{c.colSaved}</th><th scope="col">{c.colDeleted}</th><th scope="col" class="num">{c.colSize}</th></> : null}
              <th scope="col"><span class="sr-only">{t.life.keep}</span></th>
            </tr>
          </thead>
          <tbody>
            {list.map((r) => <RowView key={r.key} r={r} selectable={selectable} p={p} />)}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function RowView({ r, selectable, p }: { r: Row; selectable: boolean; p: GroupProps }) {
  const c = t.cleanFlow;
  const st = p.stateOf(r);
  const isOpen = p.open.has(r.key);
  const detail = (r.kind === "ready" || r.kind.startsWith("ask")) && r.kind !== "missing";
  const cols = 3 + (selectable ? 1 : 0) + (selectable && r.kind !== "missing" ? 3 : 0);
  return (
    <>
      <tr key={r.key} aria-selected={selectable ? p.selected(r) : undefined} class={`cl-row cl-${st}`}>
        {selectable ? <td class="pick"><input type="checkbox" aria-label={c.select(r.name)} checked={p.selected(r)} disabled={st !== "ready"} onChange={() => p.onToggle(r)} /></td> : null}
        <th scope="row">{r.name}<div class="small muted">{r.project} · <span class="mono">{r.path}</span></div></th>
        <td>
          {r.kind === "blocked" || r.kind === "kept" ? <>{r.why}{r.unblock ? <div class="cl-unblock"><strong>{c.unblock}.</strong> {r.unblock.text}{r.unblock.cmd ? <> <code class="mono">{r.unblock.cmd}</code> <CopyCommand command={r.unblock.cmd} label={c.copyCmd} /></> : null}</div> : null}</>
            : <>{c.idleDays(r.idle)}<div class="small muted">{r.why}</div></>}
        </td>
        {selectable && r.kind !== "missing" ? (
          <>
            <td>{r.changes > 0 ? <span class="cl-safe">{c.saves(r.changes)}</span> : <span class="muted small">{c.nothingToSave}</span>}</td>
            <td>{r.ignoredN > 0 ? <span class="cl-del mono">{r.ignored.join(", ")}</span> : <span class="muted small">{c.nothingDeleted}</span>}</td>
            <td class="num">{sizeText(r.size)}</td>
          </>
        ) : null}
        <td class="cl-actions">
          {detail ? <button type="button" class="btn sm" aria-expanded={isOpen} onClick={() => p.onOpen(r.key)}>{isOpen ? c.hideFiles : c.viewFiles}</button> : null}
          {r.w && r.kind !== "blocked" && r.kind !== "kept" ? <button type="button" class="btn sm" onClick={() => p.onKeep(r)}>{c.keepThis}</button> : null}
        </td>
      </tr>
      {detail && isOpen ? (
        <tr class="cl-detail">
          <td colSpan={cols}>
            <div class="cl-two">
              <div class="cl-box cl-box-safe">
                <strong>{c.detailSave}</strong>
                <p class="small">{r.changes > 0 ? c.detailSaveText(r.changes, r.name) : c.nothingToSave}</p>
              </div>
              <div class={`cl-box ${r.ignoredN > 0 ? "cl-box-del" : ""}`}>
                <strong>{c.detailDelete}</strong>
                {r.ignoredN > 0 ? <p class="small mono">{r.ignored.join(" · ")}{r.ignoredN > r.ignored.length ? ` ${c.detailOnly(r.ignoredN - r.ignored.length)}` : ""}</p> : <p class="small">{c.detailNone}</p>}
              </div>
            </div>
            <p class="small">{c.openFolder} <span class="mono">{r.path}</span> <CopyCommand command={r.path} /></p>
          </td>
        </tr>
      ) : null}
    </>
  );
}

function ConfirmRemove({ rows, bytes, saves, delBranches, onDelBranches, onClose, onDone, onError }: {
  rows: Row[]; bytes: number; saves: number; delBranches: boolean; onDelBranches: (v: boolean) => void; onClose: () => void; onDone: () => void; onError: (e: ApiError | undefined) => void;
}) {
  const c = t.cleanFlow;
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | undefined>();
  const projects = [...new Set(rows.map((r) => r.project))];
  const ignoredRows = rows.filter((r) => r.ignoredN > 0);
  const deletable = rows.filter((r) => r.branchDeletable).length;
  const match = text === c.cfWord;
  const cmd = theoCmd("worktree", "park-inactive", projects[0] ?? "", "--save-changes", "--max-changes", String(MAX_SAVED), ignoredRows.length > 0 && "--include-ignored", "--rescue-detached", delBranches && "--delete-branches",
    ...rows.filter((r) => r.kind !== "missing").flatMap((r) => ["--only", r.name]), "--confirm", projects[0] ?? "");
  const submit = async (e: Event) => {
    e.preventDefault();
    if (!match) return;
    setBusy(true);
    setError(undefined);
    onError(undefined);
    try {
      let last: ParkRunRef | null = null;
      for (const project of projects) {
        const mine = rows.filter((r) => r.project === project);
        const gone = mine.filter((r) => r.kind === "missing");
        const park = mine.filter((r) => r.kind !== "missing");
        if (gone.length > 0) await api.post<CleanReport>(`${projectPath(project)}/clean`, { paths: gone.map((r) => r.path), confirm: project });
        if (park.length > 0) {
          const options = { include_ignored: park.some((r) => r.ignoredN > 0), delete_branches: delBranches };
          const op = await api.post<Operation>(`${projectPath(project)}/park-inactive`, { save_changes: true, rescue_detached: true, max_changes: MAX_SAVED, ...options, paths: park.map((r) => r.path), confirm: project });
          const ref: ParkRunRef = { project, opId: op.id, saves: Object.fromEntries(park.map((r) => [r.path, r.changes])), options };
          // Projects are done one after the other: the window follows the last one, the earlier ones are waited for.
          if (last) await waitFor(last);
          last = ref;
        }
      }
      announce(c.started);
      if (last) showParkRun(last);
      onDone();
    } catch (err) {
      const ae = err instanceof ApiError ? err : new ApiError(0, { code: "internal", message: String(err) });
      setError(ae);
      onError(undefined);
      setBusy(false);
    }
  };
  return (
    <Modal title={c.confirmTitle(rows.length, projects.join(", "))} onClose={onClose} danger wide>
      <form onSubmit={submit} class="stack-sm">
        <ol class="cl-steps cl-steps-sm" aria-label={c.stepsLabel}>{c.steps.map((s, i) => <li key={s} aria-current={i === 1 ? "step" : undefined} class={i === 1 ? "on" : i < 1 ? "done" : ""}><span class="cl-step-n">{i + 1}</span>{s}</li>)}</ol>
        <div class="cl-block cl-block-ready"><h3><span class="cl-n">1</span> {c.cfRemove} <span class="small muted">· {c.cfFrees(sizeText(bytes))}</span></h3>
          <p class="mono small">{rows.map((r) => r.name).join(" · ")}</p></div>
        {saves > 0 ? <div class="cl-block"><h3><span class="cl-n">2</span> {c.cfSave}</h3><p class="small">{c.cfSaveText(saves)}</p></div> : null}
        {ignoredRows.length > 0 ? (
          <div class="cl-block cl-block-warn"><h3><span class="cl-n">3</span> {c.cfDelete}</h3>
            <ul>{ignoredRows.map((r) => <li key={r.key}>{c.cfDeleteIn(r.ignored.join(", "), r.name)}</li>)}</ul>
            <p class="small">{c.cfDeleteWhy}</p></div>
        ) : null}
        <div class="cl-block"><h3><span class="cl-n">4</span> {c.cfKeep}</h3><p class="small">{c.cfKeepText}</p></div>
        {deletable > 0 ? <label class="row"><input type="checkbox" checked={delBranches} onChange={(e) => onDelBranches((e.currentTarget as HTMLInputElement).checked)} /> {c.cfBranches(deletable, rows.length)}</label> : null}
        <div class="field">
          <label for="cl-word">{c.cfType(c.cfWord)}</label>
          <input id="cl-word" class="input mono" value={text} autocomplete="off" autocapitalize="off" spellcheck={false} onInput={(e) => setText((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <ErrorBanner error={error} title={t.confirm.serverRejected} />
        <div class="dialog-actions">
          <CopyCommand command={cmd} label={c.copyCmd} />
          <span class="grow" />
          <button type="button" class="btn" autoFocus onClick={onClose}>{t.app.cancel}</button>
          <button type="submit" class="btn danger solid" disabled={!match || busy}>{c.cfConfirm(rows.length)}</button>
        </div>
      </form>
    </Modal>
  );
}

/** Waits for a bulk park to end (used when several projects are removed in one go). */
async function waitFor(ref: ParkRunRef): Promise<void> {
  for (let i = 0; i < 600; i++) {
    const r = await api.get<{ operation: Operation }>(`${projectPath(ref.project)}/park-inactive/${ref.opId}`);
    if (!["queued", "running", "cancelling"].includes(r.operation.state)) return;
    await new Promise((res) => setTimeout(res, 700));
  }
}

function Leftovers({ data, onChanged, onError }: { data: Plan[]; onChanged: () => void; onError: (e: ApiError | undefined) => void }) {
  const c = t.cleanFlow;
  const leftovers = data.flatMap((p) => p.clean.leftovers.map((f) => ({ p, f })));
  if (leftovers.length === 0) return null;
  return (
    <section class="stack-sm" aria-labelledby="cl-left">
      <h3 id="cl-left">{c.leftoversTitle(leftovers.length)}</h3>
      <p class="small">{c.leftoversHint}</p>
      <ul>{leftovers.map(({ p, f }) => <li key={f.id}><span class="mono">{f.subject}</span> · {p.project} · {f.kind}{f.fix && !f.forgettable ? <> · <code>{f.fix}</code></> : null}</li>)}</ul>
      {leftovers.some(({ f }) => f.forgettable) ? (
        <button type="button" class="btn sm" onClick={() => void api.post("/reconcile/apply", { ids: leftovers.filter(({ f }) => f.forgettable).map(({ f }) => f.id) }).then(onChanged, (e) => onError(e instanceof ApiError ? e : undefined))}>{c.removeLeftovers}</button>
      ) : null}
    </section>
  );
}

function Threshold({ project, days, onSaved }: { project: string; days: number; onSaved: () => void }) {
  const [value, setValue] = useState(String(days));
  const [error, setError] = useState<ApiError | undefined>();
  useEffect(() => setValue(String(days)), [days]);
  const save = async () => {
    const n = Number(value);
    if (!Number.isInteger(n) || n < 1 || n > 90 || n === days) { setValue(String(days)); return; }
    try {
      await api.patch(projectPath(project), { inactive_after_days: n });
      announce(t.cleanup.thresholdSaved(n));
      onSaved();
    } catch (e) {
      setError(e instanceof ApiError ? e : undefined);
    }
  };
  return (
    <div class="field">
      <label for="cl-days">{t.cleanFlow.thresholdLabel(project)}</label>
      <input id="cl-days" class="input auto" type="number" min={1} max={90} value={value} onInput={(e) => setValue((e.currentTarget as HTMLInputElement).value)} onBlur={() => void save()} onKeyDown={(e) => { if (e.key === "Enter") void save(); }} />
      <ErrorBanner error={error} />
    </div>
  );
}

