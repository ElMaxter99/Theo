// Health = the reconcile report (view-phase 2.6). One small store keeps the last report for the sidebar indicator and
// the Health page. It is refreshed when the page opens, when the server says something changed (debounced) and on demand.
import { useEffect, useState } from "preact/hooks";
import { api, ApiError } from "../api/client.ts";
import { events } from "../api/events.ts";
import type { Finding, ReconcileReport } from "../api/types.ts";

export interface HealthState {
  report?: ReconcileReport;
  loading: boolean;
  error?: ApiError;
}

let state: HealthState = { loading: false };
const subs = new Set<() => void>();
let inflight: Promise<void> | null = null;
let timer: number | undefined;

function set(next: HealthState) {
  state = next;
  subs.forEach((fn) => fn());
}

export const getHealth = (): HealthState => state;

/** Id used to send a finding back to the server: the server's own `id` when it sends one, else a stable derived key. */
export function findingId(f: Finding): string {
  return f.id ?? [f.kind, f.project ?? "", f.subject, f.worktree ?? ""].join("|");
}

export function refreshHealth(): Promise<void> {
  if (inflight) return inflight;
  set({ ...state, loading: true });
  inflight = api.get<ReconcileReport>("/reconcile").then(
    (report) => set({ report: { findings: report.findings ?? [], docker_error: report.docker_error }, loading: false }),
    (e) => set({ ...state, loading: false, error: e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }) }),
  ).finally(() => { inflight = null; });
  return inflight;
}

function later() {
  if (timer !== undefined) clearTimeout(timer);
  timer = window.setTimeout(() => { timer = undefined; void refreshHealth(); }, 1500);
}

export function startHealthStore(): () => void {
  void refreshHealth();
  const offA = events.on("state.changed", later);
  const offB = events.on("reconcile.stale", later);
  return () => { offA(); offB(); if (timer !== undefined) clearTimeout(timer); };
}

export function useHealth(): HealthState {
  const [, setN] = useState(0);
  useEffect(() => {
    const fn = () => setN((n) => n + 1);
    subs.add(fn);
    return () => { subs.delete(fn); };
  }, []);
  return state;
}

/** Findings grouped by kind, in the order the server sent them. */
export function groupFindings(list: Finding[]): { kind: string; items: Finding[] }[] {
  const out: { kind: string; items: Finding[] }[] = [];
  for (const f of list) {
    let g = out.find((x) => x.kind === f.kind);
    if (!g) out.push((g = { kind: f.kind, items: [] }));
    g.items.push(f);
  }
  return out;
}

