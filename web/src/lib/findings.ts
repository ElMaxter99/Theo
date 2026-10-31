// Pure helpers for the reconcile report (unit-tested).
import type { Finding } from "../api/types.ts";

/** Id used to send a finding back to the server: the server's own `id` when it sends one, else a stable derived key. */
export function findingId(f: Finding): string {
  return f.id ?? [f.kind, f.project ?? "", f.subject, f.worktree ?? ""].join("|");
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

