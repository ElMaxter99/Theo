// V-14 agent chip: what the coding agent of a worktree is doing. Text and icon, never colour alone. A tag says
// "estimated" when the state comes from the running processes and not from the agent's hooks.
import type { AgentState } from "../api/types.ts";
import { Icon } from "./Icon.tsx";
import type { IconName } from "./Icon.tsx";
import { t } from "../strings.ts";

const SHAPE: Record<AgentState["state"], { icon: IconName; tone: string }> = {
  working: { icon: "busy", tone: "info" },
  waiting: { icon: "partial", tone: "warn" },
  done: { icon: "ok", tone: "ok" },
};

/** An agent that can still be using the worktree. */
export const agentAlive = (a: AgentState | undefined): boolean => !!a && (a.state === "working" || a.state === "waiting");

export function AgentBadge({ agent }: { agent: AgentState | undefined }) {
  if (!agent) return null;
  const shape = SHAPE[agent.state];
  const estimated = agent.source === "process";
  return (
    <span class="row tight">
      <span class={`badge ${shape.tone}`} data-testid="agent-badge">
        <Icon name={shape.icon} />
        <span>{t.agent.state[agent.state] ?? agent.state}</span>
      </span>
      {estimated ? <span class="tag" title={t.agent.estimatedHint}>{t.agent.estimated}</span> : null}
    </span>
  );
}
