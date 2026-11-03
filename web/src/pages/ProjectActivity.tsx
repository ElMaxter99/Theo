// V-07 Project › Activity: what Theo ran on every instance of the project.
import { useEffect } from "preact/hooks";
import { projectPath } from "../api/client.ts";
import { HistoryList } from "./tabs/HistoryTab.tsx";
import { ProjectHeader } from "./ProjectHeader.tsx";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

export function ProjectActivity({ project }: { project: string }) {
  useEffect(() => setTitle([t.projectTabs.activity, project]), [project]);
  return (
    <>
      <ProjectHeader project={project} tab="activity" />
      <HistoryList base={`${projectPath(project)}/history`} project={project} showInstance />
    </>
  );
}
