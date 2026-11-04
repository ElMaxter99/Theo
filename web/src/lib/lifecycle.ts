// Lifecycle of a workspace (ADR-0016): texts for the Git and Activity columns, groups and counts. Shared by the
// instances of a project (V-03), the instance summary (V-05), cleanup (V-08) and home (V-01).
import type { Lifecycle, LifeState, StatusProject } from "../api/types.ts";
import { intlLocale } from "./locale.ts";
import { t } from "../strings.ts";

/** Order of the groups of V-03. */
export const STATE_ORDER: LifeState[] = ["in-use", "unpushed", "pushed", "new", "merged", "inactive", "missing", "kept"];

export type LifeChip = "all" | "ongoing" | "toClose" | "inactive";
export function lifeChip(state: LifeState | undefined): LifeChip | null {
  switch (state) {
    case "in-use": case "unpushed": case "pushed": case "new": return "ongoing";
    case "merged": case "missing": return "toClose";
    case "inactive": return "inactive";
    default: return null;
  }
}

/** "4 min", "3 h", "12 days". */
export function since(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "";
  const ms = now - new Date(iso).getTime();
  if (Number.isNaN(ms)) return "";
  const min = Math.max(0, Math.floor(ms / 60000));
  if (min < 60) return t.life.minutes(Math.max(1, min));
  const h = Math.floor(min / 60);
  if (h < 24) return t.life.hours(h);
  return t.life.days(Math.floor(h / 24));
}

export function shortDate(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleDateString(intlLocale, { day: "numeric", month: "short" });
}

function sameDay(iso: string, now = new Date()): boolean {
  const d = new Date(iso);
  return d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
}

/** The Activity column: "commit · 3 days ago", "created · today", "until 1 Dec · «note»" for a kept one. */
export function activityText(l: Lifecycle): string {
  if (l.state === "kept") {
    const parts = [l.kept_until ? t.life.until(shortDate(l.kept_until)) : t.life.state.kept!];
    if (l.keep_note) parts.push(`«${l.keep_note}»`);
    return parts.join(" · ");
  }
  if (!l.activity_at) return "—";
  if (l.activity_source === "created" && sameDay(l.activity_at)) return t.life.createdToday;
  return t.life.ago(t.life.source[l.activity_source ?? ""] ?? l.activity_source ?? "", since(l.activity_at));
}

/** The Git column: changes, commits and where they are. */
export function gitText(l: Lifecycle, main = false): string {
  const parts: string[] = [];
  if (l.state === "missing") return t.life.gone;
  if (l.changes > 0) parts.push(t.life.changes(l.changes));
  if (l.state === "merged") {
    parts.push(l.merged_by === "content" ? t.life.mergedSquash : t.life.mergedInto(l.base ?? ""));
    return parts.join(" · ");
  }
  if (main) return t.life.upToDate;
  if (l.ahead > 0) {
    if (l.changes > 0 && l.unpushed > 0) parts.push(t.life.ahead(l.unpushed));
    else parts.push(t.life.commits(l.ahead));
    if (l.changes === 0) {
      if (!l.upstream || l.upstream_gone) parts.push(t.life.noUpstream);
      else if (l.unpushed > 0) parts.push(t.life.notPushed(l.unpushed));
      else parts.push(t.life.pushed);
    }
  } else if (l.changes === 0 && l.base) {
    parts.push(t.life.from(l.base));
  }
  return parts.join(" · ") || "—";
}

export function sizeText(bytes: number | undefined): string {
  if (!bytes || bytes <= 0) return "—";
  if (bytes < 1 << 20) return `${Math.max(1, Math.round(bytes / 1024))} KB`;
  if (bytes < 1 << 30) return `${Math.round(bytes / (1 << 20))} MB`;
  return `${(bytes / (1 << 30)).toLocaleString(intlLocale, { maximumFractionDigits: 1 })} GB`;
}

/** Counts for the "Forgotten work" card of home and the summary of `theo`. */
export function forgotten(projects: StatusProject[]): { close: number; inactive: number; idle: number } {
  let close = 0, inactive = 0, idle = 0;
  for (const p of projects) {
    for (const w of p.worktrees) {
      const s = w.lifecycle?.state;
      if (s === "merged" || s === "missing") close++;
      if (s === "inactive") inactive++;
      if ((w.warnings ?? []).some((x) => x.kind === "instance-idle")) idle++;
    }
  }
  return { close, inactive, idle };
}
