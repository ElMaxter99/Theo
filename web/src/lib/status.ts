// Single table that maps every state enum of the API to text + icon shape + tone.
// A state is never conveyed by colour alone (view-phase.md section 4).
import { t } from "../strings.ts";
import type { InstanceState, Worktree, Container, Operation } from "../api/types.ts";
import type { IconName } from "../components/Icon.tsx";

export type Tone = "ok" | "warn" | "error" | "info" | "neutral";

export interface StatusView {
  label: string;
  icon: IconName;
  tone: Tone;
  spin?: boolean;
}

export function containerCounts(w: Pick<Worktree, "containers">): { running: number; total: number } | null {
  const c = w.containers;
  if (!c) return null;
  if (Array.isArray(c)) return { running: c.filter((x: Container) => x.state === "running").length, total: c.length };
  return c;
}

const base: Record<InstanceState, StatusView> = {
  running: { label: t.state.running, icon: "running", tone: "ok" },
  partial: { label: t.state.partial, icon: "partial", tone: "warn" },
  unhealthy: { label: t.state.unhealthy, icon: "unhealthy", tone: "error" },
  stopped: { label: t.state.stopped, icon: "stopped", tone: "neutral" },
  absent: { label: t.state.absent, icon: "absent", tone: "neutral" },
  unknown: { label: t.state.unknown, icon: "unknown", tone: "neutral" },
};

/** Label for an operation in progress ("Starting…"). */
export function busyLabel(kind: string | undefined): string {
  switch (kind) {
    case "up": return t.state.starting;
    case "down": return t.state.stopping;
    case "restart": return t.state.restarting;
    default: return t.state.working;
  }
}

export function stateView(state: InstanceState | string, counts?: { running: number; total: number } | null, busyKind?: string | null): StatusView {
  if (busyKind) return { label: busyLabel(busyKind), icon: "busy", tone: "info", spin: true };
  const v = base[state as InstanceState] ?? base.unknown;
  if (state === "partial" && counts) return { ...v, label: t.state.partialWith(counts.running, counts.total) };
  return v;
}

export function worktreeStatus(w: Worktree, busyKind?: string | null): StatusView {
  return stateView(w.state, containerCounts(w), busyKind ?? w.busy?.kind ?? null);
}

export function portStatusView(status: string): StatusView {
  const label = t.portStatus[status] ?? status;
  switch (status) {
    case "free": return { label, icon: "ok", tone: "ok" };
    case "running": return { label, icon: "running", tone: "ok" };
    case "in use": return { label, icon: "portbusy", tone: "error" };
    case "reassigned": return { label, icon: "restart", tone: "info" };
    case "kept": return { label, icon: "lock", tone: "neutral" };
    case "random": return { label, icon: "dot", tone: "neutral" };
    case "range": return { label, icon: "dot", tone: "neutral" };
    default: return { label, icon: "unknown", tone: "neutral" };
  }
}

export function volumeStatusView(status: string): StatusView {
  const label = t.volumes.status[status] ?? status;
  switch (status) {
    case "in use": return { label, icon: "running", tone: "ok" };
    case "kept": return { label, icon: "lock", tone: "info" };
    case "external": return { label, icon: "external", tone: "neutral" };
    default: return { label, icon: "unknown", tone: "neutral" };
  }
}

export function operationStateView(op: Pick<Operation, "state">): StatusView {
  const label = t.ops.state[op.state] ?? op.state;
  switch (op.state) {
    case "queued": return { label, icon: "dot", tone: "neutral" };
    case "running": return { label, icon: "busy", tone: "info", spin: true };
    case "cancelling": return { label, icon: "busy", tone: "warn", spin: true };
    case "succeeded": return { label, icon: "ok", tone: "ok" };
    case "failed": return { label, icon: "error", tone: "error" };
    case "cancelled": return { label, icon: "stopped", tone: "neutral" };
    default: return { label, icon: "unknown", tone: "neutral" };
  }
}

export const isActiveOp = (op: Pick<Operation, "state">): boolean => op.state === "queued" || op.state === "running" || op.state === "cancelling";

/** Text of an operation error, whatever shape the server sent. */
export function opErrorText(op: Pick<Operation, "error">): string {
  const e = op.error;
  if (!e) return "";
  return typeof e === "string" ? e : (e.message ?? e.code ?? "");
}
