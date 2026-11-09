// Progress of a bulk park (V-08 Cleanup): the server removes the worktrees one by one in the background, and this
// window asks it how far it is, so a long run is never a button that stays disabled with nothing to look at.
import { useEffect, useRef, useState } from "preact/hooks";
import { api, projectPath } from "../api/client.ts";
import type { ApiError } from "../api/client.ts";
import type { ParkInactiveEntry, ParkInactiveRun } from "../api/types.ts";
import { announce } from "../lib/live.ts";
import { showParkRun, useParkRun } from "../lib/parkRun.ts";
import { t } from "../strings.ts";
import { Modal } from "./Dialog.tsx";
import { Icon } from "./Icon.tsx";
import { ErrorBanner } from "./ui.tsx";

/** Why a worktree stayed, in words. */
export function parkReason(e: ParkInactiveEntry): string {
  const c = t.cleanup;
    switch (e.reason_code) {
      case "changes": return c.pickReasonChanges(e.changes);
      case "too-many-changes": return c.pickReasonTooMany(e.changes);
      case "ignored": return c.pickReasonIgnored((e.ignored_names ?? []).join(", "));
      case "no-branch": return c.pickReasonNoBranch;
      case "clone": return c.pickReasonClone;
      case "locked": return c.pickReasonLocked;
      case "agent": return c.pickReasonAgent;
      case "not-inactive": return c.pickReasonNotInactive;
      default: return e.reason ?? "";
    }
  }

const POLL_MS = 700;
const active = (s?: string) => s === "queued" || s === "running" || s === "cancelling";

export function ParkProgress({ project, opId, onClose }: { project: string; opId: string; onClose: () => void }) {
  const c = t.cleanup;
  const [run, setRun] = useState<ParkInactiveRun | undefined>();
  const [error, setError] = useState<ApiError | undefined>();
  const announced = useRef(false);

  useEffect(() => {
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const tick = async () => {
      try {
        const r = await api.get<ParkInactiveRun>(`${projectPath(project)}/park-inactive/${opId}`);
        if (stop) return;
        setRun(r);
        setError(undefined);
        if (!active(r.operation.state)) return;
      } catch (e) {
        if (stop) return;
        setError(e as ApiError);
      }
      timer = setTimeout(tick, POLL_MS);
    };
    void tick();
    return () => { stop = true; if (timer) clearTimeout(timer); };
  }, [project, opId]);

  const finished = !!run && !active(run.operation.state);
  const parked = run?.entries.filter((e) => e.parked).length ?? 0;
  useEffect(() => {
    if (finished && !announced.current) {
      announced.current = true;
      announce(c.progResult(parked, run?.total ?? 0));
    }
  }, [finished]);

  const total = run?.total ?? 0;
  const done = run?.done ?? 0;
  const failedOp = finished && run?.operation.state !== "succeeded";
  const opError = run?.operation.error;
  const opMessage = typeof opError === "string" ? opError : opError?.message;
  return (
    <Modal title={c.progTitle} onClose={onClose}>
      <div class="stack-sm" aria-live="off">
        {!run || (total === 0 && !finished) ? (
          <p class="row"><Icon name="busy" size={16} /> {c.progPreparing}</p>
        ) : (
          <>
            <p class="row"><strong>{c.progCount(done, total)}</strong>{!finished ? <Icon name="busy" size={16} /> : null}</p>
            <progress max={Math.max(total, 1)} value={done} aria-label={c.progCount(done, total)} style={{ width: "100%" }} />
            {!finished && run.current ? <p class="small">{c.progNow} <span class="mono">{run.branch || run.current}</span></p> : null}
          </>
        )}
        {!finished ? <p class="small muted">{c.progWait}</p> : null}
        {run && run.entries.length > 0 ? (
          <ul class="affected" aria-label={c.progTitle} style={{ maxHeight: "16rem", overflow: "auto" }}>
            {run.entries.map((e) => (
              <li key={e.path}>
                <Icon name={e.parked ? "ok" : "warn"} size={14} /> <strong class="mono">{e.branch ?? e.path}</strong>{" "}
                <span class="muted">— {e.parked ? `${c.progOk}${e.branch_deleted ? ` · ${c.progBranchGone}` : ""}` : `${c.progStays}: ${parkReason(e)}`}</span>
              </li>
            ))}
          </ul>
        ) : null}
        {finished && !failedOp ? <p><strong>{parked === total ? c.progAllDone(parked) : c.progResult(parked, total)}</strong></p> : null}
        {failedOp ? <p role="alert"><strong>{c.progStopped}</strong> {opMessage ?? run?.operation.state}</p> : null}
        <ErrorBanner error={error} />
      </div>
      <div class="dialog-actions">
        <button type="button" class="btn" autoFocus onClick={onClose}>{finished ? c.progClose : c.progHide}</button>
      </div>
    </Modal>
  );
}

/** The window of the bulk park in course, wherever the page that started it is; closing it runs onClosed (reload). */
export function ParkRunWindow({ onClosed }: { onClosed: () => void }) {
  const run = useParkRun();
  if (!run) return null;
  return <ParkProgress project={run.project} opId={run.opId} onClose={() => { showParkRun(null); onClosed(); }} />;
}
