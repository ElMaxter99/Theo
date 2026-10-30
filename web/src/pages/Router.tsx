import { useEffect } from "preact/hooks";
import type { Meta } from "../api/types.ts";
import type { Route } from "../lib/router.ts";
import { Projects } from "./Projects.tsx";
import { AddProject } from "./AddProject.tsx";
import { ProjectPanel } from "./ProjectPanel.tsx";
import { InstanceDetail } from "./InstanceDetail.tsx";
import { Wizard } from "./Wizard.tsx";
import { SharedServices } from "./SharedServices.tsx";
import { Health } from "./Health.tsx";
import { Settings } from "./Settings.tsx";
import { PageHead } from "../components/ui.tsx";
import { href } from "../lib/router.ts";

export function setTitle(parts: string[]): void {
  document.title = [...parts.filter(Boolean), "Theo"].join(" · ");
}

export function Router({ route, meta, onRefreshMeta }: { route: Route; meta: Meta | undefined; onRefreshMeta: () => void }) {
  switch (route.name) {
    case "projects": return <Projects meta={meta} />;
    case "add": return <AddProject />;
    case "project": return <ProjectPanel project={route.project} onRefreshMeta={onRefreshMeta} />;
    case "wizard": return <Wizard project={route.project} />;
    case "shared": return <SharedServices project={route.project} />;
    case "health": return <Health />;
    case "settings": return <Settings meta={meta} />;
    case "detail": return <InstanceDetail project={route.project} wt={route.wt} tab={route.tab} />;
    default: return <NotFound />;
  }
}

function NotFound() {
  useEffect(() => setTitle(["Not found"]), []);
  return (
    <>
      <PageHead title="Page not found" />
      <p><a href={href.projects()}>Go to the projects</a></p>
    </>
  );
}
