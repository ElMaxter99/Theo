// Toasts: when an operation ends, a short note in the corner with a link to the instance. Screen readers already
// hear it through the live region (ops/store.ts), so the toasts are not a live region themselves. A success goes
// away after a few seconds; a failure stays until it is closed.
import { useEffect, useState } from "preact/hooks";
import type { Operation } from "../api/types.ts";
import { href } from "../lib/router.ts";
import { opErrorText } from "../lib/status.ts";
import { kindLabel, nameOf, onOperationFinished } from "../ops/store.ts";
import { Icon } from "./Icon.tsx";
import { t } from "../strings.ts";

export function Toasts() {
  const [list, setList] = useState<Operation[]>([]);
  useEffect(() => onOperationFinished((op) => {
    setList((l) => [op, ...l.filter((x) => x.id !== op.id)].slice(0, 3));
    if (op.state === "succeeded") window.setTimeout(() => setList((l) => l.filter((x) => x.id !== op.id)), 6000);
  }), []);
  if (list.length === 0) return null;
  return (
    <div class="toasts">
      {list.map((op) => {
        const ok = op.state === "succeeded";
        const name = nameOf(op.worktree) || op.project || "";
        const summary = op.result && typeof op.result === "object" ? op.result.summary : undefined;
        return (
          <div key={op.id} class={`toast ${ok ? "ok" : op.state === "failed" ? "error" : "neutral"}`} data-testid="toast">
            <Icon name={ok ? "ok" : op.state === "failed" ? "error" : "stopped"} />
            <div class="grow">
              <p><strong>{kindLabel(op.kind)} {name}</strong>: {(t.ops.state[op.state] ?? op.state).toLowerCase()}</p>
              {!ok && opErrorText(op) ? <p class="small">{opErrorText(op)}</p> : summary ? <p class="small">{summary}</p> : null}
              {op.project && op.worktree ? <p class="small"><a href={href.detail(op.project, op.worktree, ok ? "summary" : "logs")}>{ok ? t.toast.open : t.toast.seeLogs}</a></p> : null}
            </div>
            <button type="button" class="btn quiet sm" aria-label={t.toast.dismiss} onClick={() => setList((l) => l.filter((x) => x.id !== op.id))}>×</button>
          </div>
        );
      })}
    </div>
  );
}
