// Port clash preview for an instance that does not exist yet (wizard step 3).
// The API computes port plans per worktree, so the wizard asks for the plan of the project's main worktree and
// derives which fixed host ports a NEW instance of the same Compose files would find taken.
import type { PortMapping, PortPlan } from "../api/types.ts";

export interface PortClash {
  service: string;
  port: number;
  /** "instance": a Theo instance already uses it; "program": some other program does. */
  who: "instance" | "program";
}

export function clashesForNewInstance(plan: PortPlan | undefined): PortClash[] {
  const out: PortClash[] = [];
  for (const m of plan?.ports ?? []) {
    const clash = clashOf(m);
    if (clash) out.push(clash);
  }
  return out;
}

function clashOf(m: PortMapping): PortClash | null {
  // Only fixed host ports can clash; random ports and ranges are picked by Docker.
  const fixed = Number.parseInt(m.configured ?? "", 10);
  if (!Number.isFinite(fixed) || fixed <= 0) return null;
  if (m.status === "running") return { service: m.service, port: m.running || fixed, who: "instance" };
  if (m.status === "in use") return { service: m.service, port: fixed, who: "program" };
  return null;
}

/** Fixed host ports of a plan, whether they clash or not (used to say "nothing to clash" or not). */
export function hasFixedPorts(plan: PortPlan | undefined): boolean {
  return (plan?.ports ?? []).some((m) => Number.parseInt(m.configured ?? "", 10) > 0);
}
