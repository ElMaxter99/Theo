// V-13 "Open in…": a menu in the worktree row with the editor, the terminal, the folder and the proxy URL. The last
// one used goes first (remembered per project in this browser). An option that cannot work stays in the list,
// disabled, with the reason written next to it.
import { api, ApiError, projectPath } from "../api/client.ts";
import type { Worktree } from "../api/types.ts";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";
import { Icon } from "./Icon.tsx";
import { MenuButton } from "./Menu.tsx";
import type { MenuItem } from "./Menu.tsx";

export interface OpenTool { target: string; available: boolean; reason?: string }
export type OpenTarget = "editor" | "terminal" | "files" | "url";
const ORDER: OpenTarget[] = ["editor", "terminal", "files", "url"];

const key = (project: string) => `theo.open.last.${project}`;
export function lastOpened(project: string): OpenTarget | null {
  try { const v = localStorage.getItem(key(project)); return ORDER.includes(v as OpenTarget) ? (v as OpenTarget) : null; } catch { return null; }
}
function remember(project: string, target: OpenTarget) {
  try { localStorage.setItem(key(project), target); } catch { /* private window: nothing to remember */ }
}

export function OpenMenu({ project, w, name, url, tools, onError }: {
  project: string; w: Worktree; name: string; url: string | undefined; tools: OpenTool[] | undefined; onError: (e: ApiError) => void;
}) {
  const gone = !!w.prunable;
  const last = lastOpened(project);
  const items: MenuItem[] = ORDER.map((target) => {
    let reason = "";
    if (target === "url") { if (!url) reason = t.agent.noProxyUrl; }
    else if (gone) reason = t.agent.folderGone;
    else { const tool = tools?.find((x) => x.target === target); if (tool && !tool.available) reason = tool.reason || ""; }
    const label = t.agent.target[target]!;
    const text = `${label}${last === target ? ` · ${t.agent.lastUsed}` : ""}${reason ? ` — ${reason}` : ""}`;
    const base: MenuItem = { label: text, disabled: !!reason, sr: name };
    if (target === "url") return { ...base, href: url };
    return {
      ...base,
      onSelect: () => {
        remember(project, target);
        void api.post<{ tool: string }>(`${projectPath(project)}/worktrees/${w.id}/open`, { target }).then(
          (r) => announce(t.agent.opened(r.tool, name)),
          (e) => onError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) })),
        );
      },
    };
  });
  // The last one used goes first.
  if (last) { const i = ORDER.indexOf(last); if (i > 0) items.unshift(items.splice(i, 1)[0]!); }
  return (
    <MenuButton label={t.agent.openMenuFor(name)} testid={`open-${w.id}`} items={items}>
      <Icon name="folder" size={13} /> {t.agent.openMenu}
    </MenuButton>
  );
}
