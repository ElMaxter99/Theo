import { useEffect } from "preact/hooks";
import type { Meta } from "../api/types.ts";
import type { Route } from "../lib/router.ts";
import { Home } from "./Home.tsx";
import { Projects } from "./Projects.tsx";
import { AddProject } from "./AddProject.tsx";
import { ProjectPanel } from "./ProjectPanel.tsx";
import { InstanceDetail } from "./InstanceDetail.tsx";
import { SharedServices } from "./SharedServices.tsx";
import { ProjectActivity } from "./ProjectActivity.tsx";
import { ProjectSettings } from "./ProjectSettings.tsx";
import { Problems } from "./Problems.tsx";
import { Settings } from "./Settings.tsx";
import { PageHead } from "../components/ui.tsx";
import { href } from "../lib/router.ts";
import { t } from "../strings.ts";

export function setTitle(parts: string[]): void {
  document.title = [...parts.filter(Boolean), "Theo"].join(" · ");
}

export function Router({ route, meta, onRefreshMeta }: { route: Route; meta: Meta | undefined; onRefreshMeta: () => void }) {
  switch (route.name) {
    case "home": return <Home meta={meta} />;
    case "projects": return <Projects />;
    case "add": return <AddProject />;
    case "project":
      switch (route.tab) {
        case "shared": return <SharedServices project={route.project} />;
        case "activity": return <ProjectActivity project={route.project} />;
        case "settings": return <ProjectSettings project={route.project} />;
        default: return <ProjectPanel project={route.project} drawer={!!route.drawer} onRefreshMeta={onRefreshMeta} />;
      }
    case "problems": return <Problems tab={route.tab} project={route.project} />;
    case "settings": return <Settings meta={meta} section={route.section} />;
    case "detail": return <InstanceDetail project={route.project} wt={route.wt} tab={route.tab} />;
    default: return <NotFound />;
  }
}

function NotFound() {
  useEffect(() => setTitle([t.app.notFound]), []);
  return (
    <>
      <PageHead title={t.app.notFoundTitle} />
      <p><a href={href.home()}>{t.app.notFoundLink}</a></p>
    </>
  );
}
