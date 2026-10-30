// Remove-worktree flow (contract section 6): plain confirmation first (nothing is deleted yet), then, when the
// server answers 409 confirmation_required (uncommitted changes), the typed-confirmation dialog. A worktree with
// containers is refused by the server: we tell the user to stop the instance first.
import { useRef, useState } from "preact/hooks";
import { api, ApiError, wtPath } from "../api/client.ts";
import type { ApiErrorBody, Worktree } from "../api/types.ts";
import { ConfirmDialog, Modal, TypedConfirmDialog } from "./Dialog.tsx";
import { announce } from "../lib/live.ts";
import { startOperation } from "../ops/store.ts";
import { t } from "../strings.ts";

type Stage = { kind: "simple" } | { kind: "typed"; body: ApiErrorBody } | { kind: "containers"; message: string };

export function RemoveFlow({ project, wt, onClose, onDone }: { project: string; wt: Worktree; onClose: () => void; onDone: () => void }) {
  const [stage, setStage] = useState<Stage>({ kind: "simple" });
  const switching = useRef(false);
  const name = wt.branch || wt.path.split(/[\\/]/).filter(Boolean).pop() || wt.id;
  const url = `${wtPath(project, wt.id)}/remove`;

  const done = () => { announce(t.remove.removed(name)); onDone(); };
  const close = () => { if (switching.current) { switching.current = false; return; } onClose(); };

  if (stage.kind === "simple") {
    return (
      <ConfirmDialog title={t.remove.title(name)} body={t.remove.body} confirmLabel={t.remove.confirm} onClose={close}
        onConfirm={async () => {
          try {
            await api.post(url, {});
            done();
          } catch (e) {
            if (e instanceof ApiError && e.code === "confirmation_required") { switching.current = true; setStage({ kind: "typed", body: e.body }); return; }
            if (e instanceof ApiError && e.code === "instance_has_containers") { switching.current = true; setStage({ kind: "containers", message: e.message }); return; }
            throw e;
          }
        }}>
        <p class="mono small">{wt.path}</p>
      </ConfirmDialog>
    );
  }
  if (stage.kind === "typed") {
    const b = stage.body;
    return (
      <TypedConfirmDialog
        title={t.remove.typed(name)}
        lead={t.remove.typedLead}
        expected={b.confirm?.expected ?? name}
        prompt={b.confirm?.prompt}
        affected={b.affected}
        preserved={b.preserved}
        confirmLabel={t.remove.confirm}
        onClose={onClose}
        onConfirm={async (text) => { await api.post(url, { confirm: text }); done(); }}
      />
    );
  }
  return <HasContainers project={project} wt={wt} message={stage.message} onClose={onClose} />;
}

function HasContainers({ project, wt, message, onClose }: { project: string; wt: Worktree; message: string; onClose: () => void }) {
  return (
    <Modal title={t.remove.hasContainers} onClose={onClose}>
      <p>{message}</p>
      <p>{t.remove.hasContainersHint}</p>
      <div class="dialog-actions">
        <button type="button" class="btn" autoFocus onClick={onClose}>{t.app.close}</button>
        <button type="button" class="btn primary" onClick={async () => { try { await startOperation(project, wt.id, "down"); } finally { onClose(); } }}>{t.remove.stopFirst}</button>
      </div>
    </Modal>
  );
}
