// The status of every project (GET /status), for the sidebar, the home screen and the command palette. One request
// for the whole page, refreshed (debounced) when the server says something changed or an operation ends.
import { useEffect, useState } from "preact/hooks";
import { api, ApiError } from "../api/client.ts";
import { events } from "../api/events.ts";
import type { InstanceState, StatusProject, StatusReport, StatusWorktree } from "../api/types.ts";
import { onOperationFinished } from "../ops/store.ts";

export interface StatusState {
  report?: StatusReport;
  error?: ApiError;
  loading: boolean;
}

let state: StatusState = { loading: false };
const subs = new Set<() => void>();
let inflight: Promise<void> | null = null;
let timer: number | undefined;

function set(next: StatusState) {
  state = next;
  subs.forEach((fn) => fn());
}

export function refreshStatus(): Promise<void> {
  if (inflight) return inflight;
  set({ ...state, loading: true });
  inflight = api.get<StatusReport>("/status").then(
    (report) => set({ report, loading: false }),
    (e) => set({ ...state, loading: false, error: e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }) }),
  ).finally(() => { inflight = null; });
  return inflight;
}

function later() {
  clearTimeout(timer);
  timer = window.setTimeout(() => void refreshStatus(), 800);
}

export function startStatusStore(): () => void {
  void refreshStatus();
  const off = events.on("state.changed", later);
  const offOps = onOperationFinished(later);
  return () => { off(); offOps(); clearTimeout(timer); };
}

export function useStatus(): StatusState {
  const [, setN] = useState(0);
  useEffect(() => {
    const fn = () => setN((n) => n + 1);
    subs.add(fn);
    return () => { subs.delete(fn); };
  }, []);
  return state;
}

export const ACTIVE: InstanceState[] = ["running", "partial", "unhealthy"];

export function wtState(w: StatusWorktree): InstanceState {
  return w.instance?.instance_state ?? "unknown";
}

export function runningCount(p: StatusProject): number {
  return p.worktrees.filter((w) => ACTIVE.includes(wtState(w))).length;
}

export function attentionCount(p: StatusProject): number {
  return p.worktrees.filter((w) => wtState(w) === "partial" || wtState(w) === "unhealthy").length;
}

export function warningCount(r: StatusReport | undefined): number {
  if (!r) return 0;
  return r.warnings.length + r.projects.reduce((n, p) => n + p.warnings.length + p.worktrees.reduce((m, w) => m + w.warnings.length, 0), 0);
}

/** The name of a worktree as the lists show it: its branch, or its folder. */
export function statusName(w: StatusWorktree): string {
  return w.branch || w.path.split(/[\\/]/).filter(Boolean).pop() || w.path;
}

/** The first address of a running instance: its local domain, or a published port. */
export function statusUrl(w: StatusWorktree): string | undefined {
  const i = w.instance;
  if (!i) return undefined;
  if (i.proxy_urls && i.proxy_urls.length > 0) return i.proxy_urls[0];
  return i.ports.find((p) => p.url && p.status === "running")?.url;
}
