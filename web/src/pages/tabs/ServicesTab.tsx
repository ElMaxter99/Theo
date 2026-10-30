import { api, wtPath } from "../../api/client.ts";
import type { Container, Services, WorktreeDetail } from "../../api/types.ts";
import { Badge, ErrorBanner, Loading } from "../../components/ui.tsx";
import { Icon } from "../../components/Icon.tsx";
import { useResource } from "../../lib/hooks.ts";
import { containersOf } from "../../lib/overview.ts";
import { t } from "../../strings.ts";

function containerView(c: Container | undefined) {
  if (!c) return { label: t.services.noContainer, icon: "absent" as const, tone: "neutral" as const };
  const bad = c.health === "unhealthy" || (c.state === "exited" && (c.exit_code ?? 0) !== 0);
  if (bad) return { label: c.state === "running" ? "unhealthy" : `${c.state} (${c.exit_code})`, icon: "unhealthy" as const, tone: "error" as const };
  if (c.state === "running") return { label: c.state, icon: "running" as const, tone: "ok" as const };
  return { label: c.state, icon: "stopped" as const, tone: "neutral" as const };
}

export function ServicesTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const res = useResource<Services>(() => api.get<Services>(`${wtPath(project, w.id)}/services`), [project, w.id], { project, worktree: w.id });
  const containers = containersOf(w);
  if (res.loading && !res.data) return <Loading />;
  if (res.error) return <ErrorBanner error={res.error} />;
  const list = res.data?.services ?? [];
  if (list.length === 0) return <p class="empty">{t.services.none}</p>;
  return (
    <div class="table-wrap" tabIndex={0} role="region" aria-label={t.services.title}>
      <table class="data">
        <caption class="sr-only">{t.services.title}</caption>
        <thead>
          <tr>
            <th scope="col">{t.services.colService}</th>
            <th scope="col">{t.services.colImage}</th>
            <th scope="col">{t.services.colContainer}</th>
            <th scope="col">{t.services.colHealth}</th>
            <th scope="col">{t.services.colPorts}</th>
            <th scope="col">{t.services.colMounts}</th>
            <th scope="col">{t.services.colDepends}</th>
          </tr>
        </thead>
        <tbody>
          {list.map((s) => {
            const c = containers.find((x) => x.service === s.name);
            const v = containerView(c);
            return (
              <tr key={s.name}>
                <th scope="row">{s.name}</th>
                <td class="mono small">{s.image ?? (s.build ? t.services.build(s.build) : "—")}</td>
                <td><Badge view={v} /></td>
                <td>{c?.health ?? "—"}</td>
                <td>
                  {(s.ports ?? []).length === 0 ? "—" : (
                    <ul class="plain mono small">{(s.ports ?? []).map((p, i) => <li key={i}>{p.published ? `${p.published}:` : ""}{p.target}{p.protocol && p.protocol !== "tcp" ? `/${p.protocol}` : ""}</li>)}</ul>
                  )}
                </td>
                <td>
                  {(s.mounts ?? []).length === 0 ? "—" : (
                    <ul class="plain small">
                      {(s.mounts ?? []).map((m, i) => {
                        const sock = !!m.source && /docker\.sock$/.test(m.source);
                        return (
                          <li key={i}>
                            <span class="mono">{m.source ?? m.type}{m.target ? ` → ${m.target}` : ""}</span>
                            {m.read_only ? <span class="muted"> ({t.services.readOnly})</span> : null}
                            {m.outside_workspace || sock ? <span class="badge warn"><Icon name="warn" /> <span>{sock ? t.services.socket : t.services.outside}</span></span> : null}
                          </li>
                        );
                      })}
                    </ul>
                  )}
                </td>
                <td class="small">{(s.depends_on ?? []).join(", ") || "—"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
