// The head shared by the four screens of a project (V-03, V-06, V-07): name, folder, the main action and the tabs
// Instances · Shared · Activity · Settings (links: each is its own address).
import type { ComponentChildren } from "preact";
import { Crumbs } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { MenuButton } from "../components/Menu.tsx";
import type { MenuItem } from "../components/Menu.tsx";
import { copyText, theoCmd } from "../components/CopyCommand.tsx";
import { href, PROJECT_TABS } from "../lib/router.ts";
import type { ProjectTab } from "../lib/router.ts";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";

export function ProjectHeader({ project, path, tab, menu = [], extra }: {
  project: string; path?: string; tab: ProjectTab; menu?: (MenuItem | null | false)[]; extra?: ComponentChildren;
}) {
  const labels: Record<ProjectTab, string> = {
    instances: t.projectTabs.instances, shared: t.projectTabs.shared, activity: t.projectTabs.activity, settings: t.projectTabs.settings,
  };
  return (
    <>
      <Crumbs items={[{ label: t.projects.title, href: href.projects() }, { label: project }]} />
      <div class="page-head">
        <div>
          <h1 tabIndex={-1}>{project}</h1>
          {path ? <p class="sub mono small">{path}</p> : null}
        </div>
        <div class="row">
          {extra}
          <a class="btn primary" href={href.wizard(project)} data-testid="new-instance"><Icon name="plus" /> {t.newInstance.button}</a>
          <MenuButton label={t.app.moreFor(project)} items={[
            ...menu,
            { label: t.copy.button, onSelect: () => { void copyText(theoCmd("status", project)).then((ok) => announce(ok ? t.copy.copied : t.copy.failed)); } },
            tab !== "settings" ? { label: t.projectTabs.settings, href: href.projectSettings(project) } : null,
          ]} />
        </div>
      </div>
      <nav class="subnav" aria-label={t.projectTabs.label(project)}>
        <ul>
          {PROJECT_TABS.map((id) => (
            <li key={id}><a href={href.projectTab(project, id)} aria-current={id === tab ? "page" : undefined}>{labels[id]}</a></li>
          ))}
        </ul>
      </nav>
    </>
  );
}
