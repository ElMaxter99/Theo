// Operations (up / down / restart ...) are server-side jobs. This store keeps the list in memory,
// follows the global event channel and announces start/finish in the live region.
import { useEffect, useState } from "preact/hooks";
import { api, wtPath } from "../api/client.ts";
import { events } from "../api/events.ts";
import type { Operation, Worktree } from "../api/types.ts";
import { isActiveOp } from "../lib/status.ts";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";

let ops: Operation[] = [];
const subs = new Set<() => void>();
const names = new Map<string, string>(); // worktree id -> branch / folder name, for readable messages

function notify() {
  subs.forEach((fn) => fn());
}

export const getOps = (): Operation[] => ops;

export function rememberWorktrees(list: Pick<Worktree, "id" | "branch" | "path" | "detached">[]): void {
  for (const w of list) names.set(w.id, w.branch || w.path.split(/[\\/]/).filter(Boolean).pop() || w.id);
}
export const nameOf = (id: string | undefined): string => (id && names.get(id)) || id || "";

export function kindLabel(kind: string): string {
  return t.ops.kind[kind] ?? kind;
}

export function upsertOp(op: Operation): void {
  const prev = ops.find((o) => o.id === op.id);
  const next = prev ? { ...prev, ...op } : op;
  ops = [next, ...ops.filter((o) => o.id !== op.id)].sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? "")).slice(0, 60);
  const name = nameOf(next.worktree);
  if (!prev && isActiveOp(next)) announce(t.ops.announceStarted(t.ops.kindProgress[next.kind] ?? kindLabel(next.kind), name));
  else if (prev && isActiveOp(prev) && !isActiveOp(next)) announce(t.ops.announceDone(kindLabel(next.kind), name, t.ops.state[next.state] ?? next.state));
  notify();
}

export async function loadOps(): Promise<void> {
  try {
    const list = await api.get<Operation[] | { operations: Operation[] }>("/operations");
    const arr = Array.isArray(list) ? list : (list.operations ?? []);
    const byId = new Map(ops.map((o) => [o.id, o]));
    for (const o of arr) byId.set(o.id, { ...byId.get(o.id), ...o });
    ops = [...byId.values()].sort((a, b) => (b.started_at ?? "").localeCompare(a.started_at ?? "")).slice(0, 60);
    notify();
  } catch {
    /* the indicator simply keeps the last known list */
  }
}

export function startOpsStore(): () => void {
  void loadOps();
  const off = events.on("operation.*", (d) => {
    const o = ((d as { operation?: Operation } | undefined)?.operation ?? d) as Operation | undefined;
    if (o && typeof o.id === "string" && typeof o.state === "string") upsertOp(o);
    else void loadOps();
  });
  const offState = events.onState((s) => { if (s === "open") void loadOps(); });
  return () => { off(); offState(); };
}

export function useOps(): Operation[] {
  const [, setN] = useState(0);
  useEffect(() => {
    const fn = () => setN((n) => n + 1);
    subs.add(fn);
    return () => { subs.delete(fn); };
  }, []);
  return ops;
}

export type LifecycleKind = "up" | "down" | "restart";

export async function startOperation(project: string, wt: string, kind: LifecycleKind, body: Record<string, unknown> = {}): Promise<Operation> {
  const op = await api.post<Operation>(`${wtPath(project, wt)}/${kind}`, body, { idempotent: true });
  upsertOp({ project, worktree: wt, ...op });
  return op;
}

export const activeOpFor = (worktreeId: string): Operation | undefined => ops.find((o) => o.worktree === worktreeId && isActiveOp(o));

export async function cancelOperation(id: string): Promise<void> {
  await api.post(`/operations/${encodeURIComponent(id)}/cancel`, {});
  await loadOps();
}
