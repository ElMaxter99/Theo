// Tiny hash router. The fragment is free after the launch nonce has been exchanged and removed.
import { useEffect, useState } from "preact/hooks";

export const TABS = ["summary", "services", "ports", "logs", "variables", "volumes", "history"] as const;
export type TabId = (typeof TABS)[number];

export type Route =
  | { name: "projects" }
  | { name: "add" }
  | { name: "project"; project: string }
  | { name: "wizard"; project: string }
  | { name: "shared"; project: string }
  | { name: "health" }
  | { name: "settings" }
  | { name: "detail"; project: string; wt: string; tab: TabId }
  | { name: "notfound" };

export function parseHash(hash: string): Route {
  const h = hash.replace(/^#\/?/, "");
  if (h === "" ) return { name: "projects" };
  const parts = h.split("/").map((s) => {
    try { return decodeURIComponent(s); } catch { return s; }
  });
  if (parts[0] === "projects") return parts[1] === "new" ? { name: "add" } : { name: "projects" };
  if (parts[0] === "health" && parts.length === 1) return { name: "health" };
  if (parts[0] === "settings" && parts.length === 1) return { name: "settings" };
  if (parts[0] === "p" && parts[1]) {
    const project = parts[1];
    if (parts.length === 2) return { name: "project", project };
    if (parts[2] === "new" && parts.length === 3) return { name: "wizard", project };
    if (parts[2] === "shared" && parts.length === 3) return { name: "shared", project };
    if (parts[2] === "w" && parts[3]) {
      const tab = (TABS as readonly string[]).includes(parts[4] ?? "") ? (parts[4] as TabId) : "summary";
      return { name: "detail", project, wt: parts[3], tab };
    }
  }
  return { name: "notfound" };
}

const e = encodeURIComponent;
export const href = {
  projects: () => "#/projects",
  add: () => "#/projects/new",
  project: (p: string) => `#/p/${e(p)}`,
  health: () => "#/health",
  settings: () => "#/settings",
  shared: (p: string) => `#/p/${e(p)}/shared`,
  wizard: (p: string) => `#/p/${e(p)}/new`,
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
    const on = () => setRoute(parseHash(location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}
