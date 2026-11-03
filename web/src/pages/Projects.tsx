// V-02 Projects: one row per project with its running instances, what needs attention and the next step. The rest of
// the actions (settings, copy command, unregister) are in the row menu.
import { useEffect, useState } from "preact/hooks";
import { api, projectPath } from "../api/client.ts";
import type { Project } from "../api/types.ts";
import { ErrorBanner, Loading, PageHead } from "../components/ui.tsx";
import { ConfirmDialog } from "../components/Dialog.tsx";
import { Icon } from "../components/Icon.tsx";
import { MenuButton } from "../components/Menu.tsx";
import { copyText, theoCmd } from "../components/CopyCommand.tsx";
import { useResource } from "../lib/hooks.ts";
import { href } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { attentionCount, refreshStatus, runningCount, useStatus } from "../lib/statusStore.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function Projects() {
  const res = useResource<Project[]>(() => api.get<Project[]>("/projects"), [], { project: "*" });
  const status = useStatus();
  const [removing, setRemoving] = useState<Project | null>(null);
  useEffect(() => setTitle([t.projects.title]), []);

  if (res.loading && !res.data) return <><PageHead title={t.projects.title} /><Loading /></>;
  if (res.error && !res.data) return <><PageHead title={t.projects.title} /><ErrorBanner error={res.error} title={t.projects.loadError} /></>;
  const projects = res.data ?? [];
  if (projects.length === 0) return <><PageHead title={t.projects.title} /><p class="empty">{t.projects.none} <a href={href.add()}>{t.projects.add}</a></p></>;

  return (
    <>
      <PageHead title={t.projects.title} actions={<a class="btn primary" href={href.add()}><Icon name="plus" /> {t.projects.add}</a>} />
      <div class="table-wrap">
        <table class="data">
          <caption class="sr-only">{t.projects.title}</caption>
          <thead>
            <tr>
              <th scope="col">{t.projects.colProject}</th>
              <th scope="col">{t.projects.colInstances}</th>
              <th scope="col">{t.projects.colAttention}</th>
              <th scope="col" class="num">{t.projects.colActions}</th>
            </tr>
          </thead>
          <tbody>
            {projects.map((p) => {
              const st = status.report?.projects.find((x) => x.name === p.name);
              const running = st ? runningCount(st) : undefined;
              const attention = st ? attentionCount(st) + st.warnings.length : undefined;
              return (
                <tr key={p.name}>
                  <th scope="row"><a href={href.project(p.name)}>{p.name}</a><div class="mono small muted">{p.path}</div></th>
                  <td>
                    {st ? (
                      <span class="row">
                        <span class={`badge ${running ? "ok" : "neutral"}`}><Icon name={running ? "running" : "stopped"} /> <span>{t.projects.runningShort(running ?? 0)}</span></span>
                        <span class="small muted">{t.projects.ofTotal(st.worktrees.filter((w) => w.kind !== "bare").length)}</span>
                      </span>
                    ) : st === undefined && status.loading ? <span class="muted small">{t.app.loading}</span> : <span class="muted">—</span>}
                  </td>
                  <td>{attention ? <a class="badge warn" href={href.project(p.name)}><Icon name="warn" /> <span>{t.projects.attention(attention)}</span></a> : <span class="muted">—</span>}</td>
                  <td class="actions num">
                    <span class="row end">
                      <a class="btn sm" href={href.wizard(p.name)}><Icon name="plus" /> {t.projects.newInstance}<span class="sr-only"> {p.name}</span></a>
                      <MenuButton label={t.app.moreFor(p.name)} items={[
                        { label: t.projectTabs.settings, href: href.projectSettings(p.name) },
                        { label: t.projectTabs.activity, href: href.activity(p.name) },
                        { label: t.copy.button, onSelect: () => { void copyText(theoCmd("status", p.name)).then((ok) => announce(ok ? t.copy.copied : t.copy.failed)); } },
                        { label: t.projects.unregister, danger: true, onSelect: () => setRemoving(p) },
                      ]} />
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {removing ? (
        <ConfirmDialog
          title={t.projects.unregisterTitle(removing.name)}
          body={t.projects.unregisterBody}
          confirmLabel={t.projects.unregisterConfirm}
          onClose={() => setRemoving(null)}
          onConfirm={async () => {
            await api.del(projectPath(removing.name));
            announce(t.projects.unregistered(removing.name));
            res.reload();
            void refreshStatus();
          }}
        />
      ) : null}
    </>
  );
}
