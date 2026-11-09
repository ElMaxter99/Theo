// Cleanup, step 4: what happened to each worktree of the last bulk removal, and what to do with the ones that stayed.
import { useState } from "preact/hooks";
import { api, ApiError, projectPath } from "../api/client.ts";
import type { Operation } from "../api/types.ts";
import { CopyCommand, theoCmd } from "../components/CopyCommand.tsx";
import { ErrorBanner } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { parkReason } from "../components/ParkProgress.tsx";
import { sizeText } from "../lib/lifecycle.ts";
import type { ParkResult, ParkRunRef } from "../lib/parkRun.ts";
import { href } from "../lib/router.ts";
import { t } from "../strings.ts";

export function ParkResultPanel({ result, waiting, onDone, onRetry }: { result: ParkResult; waiting: number; onDone: () => void; onRetry: (ref: ParkRunRef) => void }) {
  const c = t.cleanFlow;
  const { run, project, options } = result;
  const entries = run.entries;
  const ok = entries.filter((e) => e.parked);
  const failed = entries.filter((e) => !e.parked);
  const freed = ok.reduce((n, e) => n + (e.size_bytes ?? 0), 0);
  const [error, setError] = useState<ApiError | undefined>();
  const [busy, setBusy] = useState(false);
  const retry = async (paths: string[]) => {
    setBusy(true);
    setError(undefined);
    try {
      const op = await api.post<Operation>(`${projectPath(project)}/park-inactive`, { save_changes: true, rescue_detached: true, max_changes: 1000, ...options, paths, confirm: project });
      onRetry({ project, opId: op.id, options });
    } catch (e) {
      setError(e instanceof ApiError ? e : undefined);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section class="stack" aria-labelledby="cl-res">
      <div class={`cl-head ${failed.length > 0 ? "cl-head-warn" : "cl-head-ok"}`} role="status">
        <h3 id="cl-res">{c.resultHead(ok.length, failed.length, entries.length)}</h3>
        <p class="small muted">{c.resultFreed(sizeText(freed))}{waiting > 0 ? ` ${c.stillWaiting(waiting)}` : ""}</p>
      </div>
      <div class="cl-tiles" role="group" aria-label={c.resultTitle}>
        <div class="cl-tile cl-ready"><span class="cl-tile-l"><Icon name="ok" size={14} /> {c.tileRemoved}</span><strong>{ok.length}</strong></div>
        <div class="cl-tile cl-blocked"><span class="cl-tile-l"><Icon name="warn" size={14} /> {c.tileFailed}</span><strong>{failed.length}</strong></div>
      </div>
      <ErrorBanner error={error} />
      <ul class="cl-results">
        {failed.map((e) => (
          <li key={e.path} class="cl-res cl-res-fail">
            <span class="cl-badge cl-badge-blocked"><Icon name="warn" size={14} /> {c.stayed}</span>
            <div class="grow"><strong>{e.branch ?? e.path}</strong> <span class="small muted mono">{e.path}</span><br />{parkReason(e)}</div>
            <button type="button" class="btn sm" disabled={busy} onClick={() => void retry([e.path])}>{c.retry}</button>
            <CopyCommand command={theoCmd("worktree", "park-inactive", project, "--save-changes", "--only", e.branch ?? e.path, "--confirm", project)} label={c.copyCmd} />
          </li>
        ))}
        {ok.map((e) => (
          <li key={e.path} class="cl-res">
            <span class="cl-badge cl-badge-ready"><Icon name="ok" size={14} /> {c.removed}</span>
            <div class="grow"><strong>{e.branch ?? e.path}</strong><br />
              <span class="small muted">{[e.saved ? c.savedAs(e.changes) : "", (e.ignored ?? 0) > 0 ? c.deletedList((e.ignored_names ?? []).join(", ")) : "", sizeText(e.size_bytes), e.branch_deleted ? c.branchDeleted : c.branchKept].filter(Boolean).join(" · ")}</span></div>
            <a class="btn sm" href={href.project(project)}>{c.resume}</a>
          </li>
        ))}
      </ul>
      <div class="row">
        <button type="button" class="btn" onClick={onDone}>{c.dismiss}</button>
      </div>
    </section>
  );
}
