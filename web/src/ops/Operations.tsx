// Global operations indicator (header) and panel. Operations run on the server and survive closing the tab.
import { useEffect, useRef, useState } from "preact/hooks";
import type { Operation } from "../api/types.ts";
import { Icon } from "../components/Icon.tsx";
import { Badge } from "../components/ui.tsx";
import { VirtualLog } from "../components/VirtualLog.tsx";
import { formatTime } from "../lib/format.ts";
import { href } from "../lib/router.ts";
import { isActiveOp, operationStateView, opErrorText } from "../lib/status.ts";
import { useLogStream } from "../lib/useLogStream.ts";
import { cancelOperation, kindLabel, nameOf, useOps } from "./store.ts";
import { t } from "../strings.ts";

export function OpsIndicator() {
  const ops = useOps();
  const active = ops.filter(isActiveOp);
  const [open, setOpen] = useState(false);
  const btn = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    const on = (e: Event) => { setOpen(true); (e as CustomEvent).preventDefault?.(); };
    window.addEventListener("theo:show-operations", on);
    return () => window.removeEventListener("theo:show-operations", on);
  }, []);
  return (
    <div class="ops-indicator">
      <button ref={btn} type="button" class="btn sm" aria-expanded={open} aria-controls="ops-panel" onClick={() => setOpen(!open)}>
        <Icon name={active.length > 0 ? "busy" : "ops"} spin={active.length > 0} />
        <span>{t.ops.indicator(active.length)}</span>
      </button>
      {open ? <OpsPanel ops={ops} onClose={() => { setOpen(false); btn.current?.focus(); }} /> : null}
    </div>
  );
}

function OpsPanel({ ops, onClose }: { ops: Operation[]; onClose: () => void }) {
  const [viewing, setViewing] = useState<string | null>(null);
  const head = useRef<HTMLHeadingElement>(null);
  useEffect(() => { head.current?.focus(); }, []);
  const active = ops.filter(isActiveOp);
  const recent = ops.filter((o) => !isActiveOp(o)).slice(0, 8);
  return (
    <section id="ops-panel" class="card ops-panel" aria-labelledby="ops-title" onKeyDown={(e) => { if (e.key === "Escape") { e.stopPropagation(); onClose(); } }}>
      <div class="row between">
        <h2 id="ops-title" tabIndex={-1} ref={head}>{t.ops.title}</h2>
        <button type="button" class="btn sm quiet" onClick={onClose}>{t.app.close}</button>
      </div>
      {active.length === 0 ? <p class="muted">{t.ops.none}</p> : <OpList ops={active} viewing={viewing} setViewing={setViewing} />}
      {recent.length > 0 ? (
        <>
          <h3>{t.ops.recent}</h3>
          <OpList ops={recent} viewing={viewing} setViewing={setViewing} />
        </>
      ) : null}
    </section>
  );
}

function OpList({ ops, viewing, setViewing }: { ops: Operation[]; viewing: string | null; setViewing: (id: string | null) => void }) {
  return (
    <ul class="ops-list">
      {ops.map((op) => <OpItem key={op.id} op={op} viewing={viewing === op.id} toggle={() => setViewing(viewing === op.id ? null : op.id)} />)}
    </ul>
  );
}

function OpItem({ op, viewing, toggle }: { op: Operation; viewing: boolean; toggle: () => void }) {
  const name = nameOf(op.worktree) || op.project || "";
  const active = isActiveOp(op);
  const err = opErrorText(op);
  const original = typeof op.error === "object" && op.error ? (op.error as { original?: string }).original : undefined;
  const [cancelling, setCancelling] = useState(false);
  return (
    <li>
      <div class="row between">
        <strong>{kindLabel(op.kind)} {op.project && op.worktree ? <a href={href.detail(op.project, op.worktree)}>{name}</a> : name}</strong>
        <Badge view={operationStateView(op)} />
      </div>
      <p class="small muted">{op.project ? `${op.project} · ` : ""}{t.ops.started} {formatTime(op.started_at)}{op.finished_at ? ` · ${t.ops.finished} ${formatTime(op.finished_at)}` : ""}</p>
      {err ? <p class="field-error"><Icon name="error" /> <span>{err}</span></p> : null}
      {original ? <details><summary>{t.banner.detailsOriginal}</summary><pre class="mono small prewrap">{original}</pre></details> : null}
      {op.state === "failed" || op.state === "cancelled" ? <p class="small muted">{op.kind === "up" || op.kind === "restart" ? t.ops.partialNote : ""}</p> : null}
      <div class="row">
        <button type="button" class="btn sm" aria-expanded={viewing} onClick={toggle}>{viewing ? t.ops.hideOutput : t.ops.output}</button>
        {active && op.cancellable !== false ? (
          <button type="button" class="btn sm danger" disabled={cancelling || op.state === "cancelling"} aria-label={t.ops.cancelNamed(`${kindLabel(op.kind)} ${name}`)}
            onClick={async () => { setCancelling(true); try { await cancelOperation(op.id); } finally { setCancelling(false); } }}>{t.ops.cancel}</button>
        ) : null}
      </div>
      {active ? <p class="hint">{t.ops.cancelNote}</p> : null}
      {viewing ? <OpOutput id={op.id} /> : null}
    </li>
  );
}

export function OpOutput({ id }: { id: string }) {
  const s = useLogStream({ path: `/operations/${encodeURIComponent(id)}/events`, enabled: true });
  const [follow, setFollow] = useState(true);
  return (
    <div>
      <VirtualLog store={s.store} version={s.version} shown={s.shown} search="" follow={follow} onFollowChange={setFollow} label={t.ops.outputLabel} />
    </div>
  );
}
