// Tiny hash router. The fragment is free after the launch nonce has been exchanged and removed.
// Routes of the 0.2 redesign (Theo-workspace design/2026-11-rediseno-vistas-y-flujos.html); the routes of 0.1 still
// open the screen that took their content (#/health, and the services, ports, variables, volumes and history tabs).
import { useEffect, useState } from "preact/hooks";

export const TABS = ["summary", "logs", "config", "data", "activity"] as const;
export type TabId = (typeof TABS)[number];

/** The tabs of 0.1 and the tab of 0.2 that shows their content now. */
const OLD_TABS: Record<string, TabId> = { services: "summary", ports: "summary", variables: "config", volumes: "data", history: "activity" };

export const PROJECT_TABS = ["instances", "shared", "activity", "settings"] as const;
export type ProjectTab = (typeof PROJECT_TABS)[number];

export const SETTINGS_SECTIONS = ["preferences", "domains", "agents", "diagnostics", "about"] as const;
export type SettingsSection = (typeof SETTINGS_SECTIONS)[number];

export type Route =
  | { name: "home" }
  | { name: "projects" }
  | { name: "add" }
  | { name: "project"; project: string; tab: ProjectTab; drawer?: boolean }
  | { name: "problems"; tab: "problems" | "cleanup" }
  | { name: "settings"; section: SettingsSection }
  | { name: "detail"; project: string; wt: string; tab: TabId }
  | { name: "notfound" };

export function parseHash(hash: string): Route {
  const h = hash.replace(/^#\/?/, "").replace(/\/$/, "");
  if (h === "") return { name: "home" };
  const parts = h.split("/").map((s) => {
    try { return decodeURIComponent(s); } catch { return s; }
  });
  if (parts[0] === "projects") {
    if (parts.length === 1) return { name: "projects" };
    if (parts[1] === "new" && parts.length === 2) return { name: "add" };
    return { name: "notfound" };
  }
  if ((parts[0] === "problems" || parts[0] === "health") && parts.length <= 2) {
    if (parts.length === 1) return { name: "problems", tab: "problems" };
    if (parts[0] === "problems" && (parts[1] === "cleanup" || parts[1] === "problems")) return { name: "problems", tab: parts[1] };
    return { name: "notfound" };
  }
  if (parts[0] === "settings" && parts.length <= 2) {
    const section = (SETTINGS_SECTIONS as readonly string[]).includes(parts[1] ?? "") ? (parts[1] as SettingsSection) : parts.length === 1 ? "preferences" : null;
    return section ? { name: "settings", section } : { name: "notfound" };
  }
  if (parts[0] === "p" && parts[1]) {
    const project = parts[1];
    if (parts.length === 2) return { name: "project", project, tab: "instances" };
    if (parts[2] === "new" && parts.length === 3) return { name: "project", project, tab: "instances", drawer: true };
    if (parts.length === 3 && (PROJECT_TABS as readonly string[]).includes(parts[2]!)) return { name: "project", project, tab: parts[2] as ProjectTab };
    if (parts[2] === "w" && parts[3] && parts.length <= 5) {
      const raw = parts[4] ?? "";
      const tab = (TABS as readonly string[]).includes(raw) ? (raw as TabId) : (OLD_TABS[raw] ?? "summary");
      return { name: "detail", project, wt: parts[3], tab };
    }
  }
  return { name: "notfound" };
}

/** The address a route of 0.1 now lives at, or "" when it is already current. */
export function canonicalHash(hash: string): string {
  const r = parseHash(hash);
  const h = hash.startsWith("#") ? hash : `#${hash}`;
  let want = "";
  if (r.name === "problems" && /^#\/?health/.test(h)) want = href.problems();
  if (r.name === "detail") {
    const raw = h.replace(/^#\/?/, "").split("/")[4];
    if (raw && raw !== r.tab) want = href.detail(r.project, r.wt, r.tab);
  }
  return want && want !== h ? want : "";
}

const e = encodeURIComponent;
export const href = {
  home: () => "#/",
  projects: () => "#/projects",
  add: () => "#/projects/new",
  project: (p: string) => `#/p/${e(p)}`,
  projectTab: (p: string, tab: ProjectTab) => (tab === "instances" ? `#/p/${e(p)}` : `#/p/${e(p)}/${tab}`),
  shared: (p: string) => `#/p/${e(p)}/shared`,
  activity: (p: string) => `#/p/${e(p)}/activity`,
  projectSettings: (p: string) => `#/p/${e(p)}/settings`,
  /** The instances of the project with the "New instance" panel open. */
  wizard: (p: string) => `#/p/${e(p)}/new`,
  problems: (tab: "problems" | "cleanup" = "problems") => (tab === "cleanup" ? "#/problems/cleanup" : "#/problems"),
  /** 0.1 name of the problems screen. */
  health: () => "#/problems",
  settings: (section: SettingsSection = "preferences") => (section === "preferences" ? "#/settings" : `#/settings/${section}`),
  detail: (p: string, wt: string, tab: TabId = "summary") => `#/p/${e(p)}/w/${e(wt)}/${tab}`,
};

export function navigate(to: string, replace = false) {
  if (replace) history.replaceState(null, "", to);
  else location.hash = to.replace(/^#/, "");
  if (replace) window.dispatchEvent(new HashChangeEvent("hashchange"));
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => parseHash(location.hash));
  useEffect(() => {
    const on = () => {
      const fixed = canonicalHash(location.hash);
      if (fixed) { history.replaceState(null, "", fixed); }
      setRoute(parseHash(location.hash));
    };
    on();
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}
