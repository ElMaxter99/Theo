// V-05 Instance › Data: the volumes, their backups (kept in Theo's data directory), restoring one, copying the data
// of another instance, and deleting the data. Restoring and copying overwrite data, so both ask for the instance name.
import { useState } from "preact/hooks";
import { api, projectPath, wtPath } from "../../api/client.ts";
import type { BackupInfo, Overview, WorktreeDetail } from "../../api/types.ts";
import { ErrorBanner, Loading } from "../../components/ui.tsx";
import { TypedConfirmDialog } from "../../components/Dialog.tsx";
import { CopyCommand, theoCmd } from "../../components/CopyCommand.tsx";
import { Icon } from "../../components/Icon.tsx";
import { useAction, useResource } from "../../lib/hooks.ts";
import { formatBytes, formatTime } from "../../lib/format.ts";
import { announce } from "../../lib/live.ts";
import { containerCounts } from "../../lib/status.ts";
import { startVolumeOperation } from "../../ops/store.ts";
import { t } from "../../strings.ts";
import { VolumesTab } from "./VolumesTab.tsx";
import { wtArg, wtName } from "../ProjectPanel.tsx";

export function DataTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const d = t.data;
  const backups = useResource<BackupInfo[]>(() => api.get<BackupInfo[]>(`${wtPath(project, w.id)}/volumes/backups`), [project, w.id], { project, worktree: w.id });
  const overview = useResource<Overview>(() => api.get<Overview>(`${projectPath(project)}/overview`), [project]);
  const [restoring, setRestoring] = useState<BackupInfo | null>(null);
  const [cloneFrom, setCloneFrom] = useState("");
  const [cloning, setCloning] = useState(false);
  const counts = containerCounts(w);
  const hasContainers = w.state !== "absent" && (counts ? counts.total > 0 : w.state !== "unknown");
  const expected = w.compose_project ?? "";
  const backup = useAction(async () => { await startVolumeOperation(project, w.id, "backup", {}); announce(d.backupStarted); });
  const others = (overview.data?.worktrees ?? []).filter((x) => x.id !== w.id && !x.prunable);
  const arg = wtArg(w);

  return (
    <div class="stack">
      <section aria-labelledby="data-vols">
        <h2 id="data-vols">{t.volumes.title}</h2>
        <VolumesTab project={project} w={w} />
      </section>

      <section class="card stack" aria-labelledby="data-backups">
        <div class="row between">
          <h2 id="data-backups">{d.backupsTitle}</h2>
          <span class="row">
            <CopyCommand command={theoCmd("volumes", "backup", project, arg ? "-w" : "", arg)} />
            <button type="button" class="btn sm" disabled={backup.busy} onClick={() => void backup.run()}><Icon name="download" /> {d.backupNow}</button>
          </span>
        </div>
        <p class="hint">{d.backupHint}</p>
        <ErrorBanner error={backup.error ?? backups.error} />
        {backups.loading && !backups.data ? <Loading /> : null}
        {backups.data && backups.data.length === 0 ? <p class="muted">{d.noBackups}</p> : null}
        {backups.data && backups.data.length > 0 ? (
          <div class="table-wrap" tabIndex={0} role="region" aria-label={d.backupsTitle}>
            <table class="data">
              <caption class="sr-only">{d.backupsTitle}</caption>
              <thead><tr><th scope="col">{d.colVolume}</th><th scope="col">{d.colWhen}</th><th scope="col" class="num">{d.colSize}</th><th scope="col" class="num">{t.panel.colActions}</th></tr></thead>
              <tbody>
                {backups.data.map((b) => (
                  <tr key={b.archive}>
                    <th scope="row" class="mono">{b.volume}<div class="small muted">{b.archive}</div></th>
                    <td class="nowrap">{formatTime(b.created_at)}</td>
                    <td class="num">{formatBytes(b.size)}</td>
                    <td class="num">
                      <button type="button" class="btn sm" disabled={hasContainers} aria-describedby={hasContainers ? "data-stop-first" : undefined} onClick={() => setRestoring(b)}>
                        {d.restore}<span class="sr-only"> {b.volume} {formatTime(b.created_at)}</span>
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {hasContainers ? <p class="hint" id="data-stop-first">{d.stopFirst}</p> : null}
      </section>

      <section class="card stack" aria-labelledby="data-clone">
        <h2 id="data-clone">{d.cloneTitle}</h2>
        <p class="hint">{d.cloneHint}</p>
        <div class="row">
          <label for="data-clone-from">{d.cloneFrom}</label>
          <select id="data-clone-from" class="input auto" value={cloneFrom} onChange={(e) => setCloneFrom((e.currentTarget as HTMLSelectElement).value)}>
            <option value="">{d.pick}</option>
            {others.map((x) => <option key={x.id} value={x.id}>{wtName(x)}</option>)}
          </select>
          <button type="button" class="btn sm" disabled={!cloneFrom || hasContainers} onClick={() => setCloning(true)}>{d.cloneButton}</button>
        </div>
      </section>

      {restoring ? (
        <TypedConfirmDialog
          title={d.restoreTitle(restoring.volume)}
          lead={d.restoreLead(restoring.archive, expected)}
          expected={expected}
          affectedTitle={d.overwritten}
          affected={[{ name: restoring.docker_volume, detail: d.replaced }]}
          preserved={[d.keepOther, d.keepBackup]}
          confirmLabel={d.restore}
          onClose={() => setRestoring(null)}
          onConfirm={async (text) => { await startVolumeOperation(project, w.id, "restore", { backup: restoring.archive, volume: restoring.volume, confirm: text, replace: true }); announce(d.restoreStarted); }}
        />
      ) : null}
      {cloning ? (
        <TypedConfirmDialog
          title={d.cloneConfirmTitle(wtName(others.find((x) => x.id === cloneFrom) ?? w))}
          lead={d.cloneLead}
          expected={expected}
          affectedTitle={d.overwritten}
          affected={[{ name: expected, detail: d.replaced }]}
          preserved={[d.keepSource]}
          confirmLabel={d.cloneButton}
          onClose={() => setCloning(false)}
          onConfirm={async (text) => { await startVolumeOperation(project, w.id, "clone", { from: cloneFrom, confirm: text, replace: true }); announce(d.cloneStarted); }}
        />
      ) : null}
    </div>
  );
}
