// Derived views of a worktree as the overview reports it.
import type { Container, PortConflict, RawContainer, UrlInfo, Worktree, WtPort } from "../api/types.ts";

/** Links of the ports a running instance publishes (only the URLs the server gives). */
export function urlsOf(w: Pick<Worktree, "ports">): UrlInfo[] {
  return (w.ports ?? []).filter((p: WtPort) => p.url && p.status === "running").map((p) => ({ service: p.service, url: p.url!, port: p.host_port }));
}

/** Fixed host ports that this instance wants but something else is using. */
export function conflictsOf(w: Pick<Worktree, "ports" | "state">): PortConflict[] {
  if (w.state === "running") return [];
  return (w.ports ?? []).filter((p) => p.status === "in use").map((p) => ({ service: p.service, port: p.host_port ?? 0 }));
}

export function shortSha(s: string | undefined): string {
  return s ? s.slice(0, 7) : "";
}

/** Containers of a detail response, with normalised names (the server sends `docker compose ps` records as they are). */
export function containersOf(w: Pick<Worktree, "containers">): Container[] {
  if (!Array.isArray(w.containers)) return [];
  return (w.containers as RawContainer[]).map((c) => ({
    name: c.Name ?? "",
    service: c.Service ?? "",
    state: (c.State ?? "").toLowerCase(),
    health: c.Health || undefined,
    status: c.Status,
    exit_code: c.ExitCode,
    publishers: (c.Publishers ?? []).map((p) => ({ target_port: p.TargetPort ?? 0, published_port: p.PublishedPort ?? 0, protocol: p.Protocol })),
  }));
}
