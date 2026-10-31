// Health (view-phase 2.6). The sidebar indicator uses the cheap summary (GET /health, cached by the server); the Health
// page loads the full reconcile report (GET /reconcile) and also updates the summary with what it sees.
// One small store keeps both. The summary is refreshed when the app starts and, debounced, when the server says that
// something changed (`state.changed`, `reconcile.stale`).
import { useEffect, useState } from "preact/hooks";
import { api, ApiError } from "../api/client.ts";
import { events } from "../api/events.ts";
import type { HealthSummary, ReconcileReport } from "../api/types.ts";

export interface HealthState {
  summary?: HealthSummary;
  report?: ReconcileReport;
  loading: boolean;
  error?: ApiError;
}

export { findingId, groupFindings } from "./findings.ts";

let state: HealthState = { loading: false };
const subs = new Set<() => void>();
let inflightSummary: Promise<void> | null = null;
let inflightReport: Promise<void> | null = null;
let timer: number | undefined;

function set(next: HealthState) {
  state = next;
  subs.forEach((fn) => fn());
}
const asError = (e: unknown): ApiError => (e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));

export const getHealth = (): HealthState => state;

export function summaryOf(report: ReconcileReport): HealthSummary {
  const findings = report.findings.length;
  return {
    status: report.docker_error ? "docker_unavailable" : findings > 0 ? "attention" : "ok",
    findings,
    forgettable: report.findings.filter((f) => f.forgettable).length,
    docker_error: report.docker_error,
  };
}

export function refreshHealth(): Promise<void> {
  if (inflightSummary) return inflightSummary;
  inflightSummary = api.get<HealthSummary>("/health").then(
    (summary) => set({ ...state, summary, error: undefined }),
    (e) => set({ ...state, error: asError(e) }),
  ).finally(() => { inflightSummary = null; });
  return inflightSummary;
}

/** Loads the full report (the Health page). */
export function refreshReport(): Promise<void> {
  if (inflightReport) return inflightReport;
  set({ ...state, loading: true });
  inflightReport = api.get<ReconcileReport>("/reconcile").then(
    (r) => {
      const report = { findings: r.findings ?? [], docker_error: r.docker_error };
      set({ ...state, report, summary: summaryOf(report), loading: false, error: undefined });
    },
    (e) => set({ ...state, loading: false, error: asError(e) }),
  ).finally(() => { inflightReport = null; });
  return inflightReport;
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
