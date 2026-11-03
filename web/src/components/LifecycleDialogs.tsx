// Park and Keep dialogs (board "V-05 Instancia › Ciclo de vida y Aparcar"). Parking takes the instance down, removes
// the folder and keeps the branch; the server refuses uncommitted changes and asks for the folder name when files Git
// ignores would be lost, which this dialog then lists before "Park anyway".
import { useState } from "preact/hooks";
import { api, ApiError, projectPath, wtPath } from "../api/client.ts";
import type { LifeWorktree } from "../api/types.ts";
import { Modal } from "./Dialog.tsx";
import { CopyCommand, theoCmd } from "./CopyCommand.tsx";
import { ErrorBanner } from "./ui.tsx";
import { Icon } from "./Icon.tsx";
import { announce } from "../lib/live.ts";
import { sizeText } from "../lib/lifecycle.ts";
import { t } from "../strings.ts";

const nameOf = (w: Pick<LifeWorktree, "branch" | "path">) => w.branch || w.path.split(/[\\/]/).filter(Boolean).pop() || w.path;
const asError = (e: unknown) => (e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));

export function ParkDialog({ project, w, onClose, onDone }: { project: string; w: LifeWorktree; onClose: () => void; onDone: () => void }) {
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | undefined>();
  const [ignored, setIgnored] = useState<{ files: string[]; confirm: string } | null>(null);
  const name = nameOf(w);
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await api.post(`${wtPath(project, w.id)}/park`, { note, confirm: ignored?.confirm ?? "" });
      announce(t.life.parkDone(name));
      onDone();
      onClose();
    } catch (err) {
      const ae = asError(err);
      if (ae.code === "confirmation_required" && ae.body.confirm?.expected) {
        setIgnored({ files: (ae.body.affected ?? []).map((a) => a.name), confirm: ae.body.confirm.expected });
      } else {
        setError(ae);
      }
      setBusy(false);
    }
  };
  return (
    <Modal title={t.life.parkTitle(name)} onClose={onClose}>
      <form onSubmit={submit} class="stack">
        <p>{t.life.parkLead}</p>
        {w.changes > 0 ? <p class="badge warn">{t.life.parkChanges(w.changes)}</p> : null}
        <ul class="plain stack-sm">
          {w.running ? <li class="row"><Icon name="ok" /> <span>{t.life.parkDown}</span></li> : null}
          <li class="row"><Icon name="ok" /> <span>{t.life.parkFolder(w.size_bytes ? sizeText(w.size_bytes) : "")}</span></li>
          <li class="row"><Icon name="ok" /> <span>{t.life.parkBranch(name, Math.max(0, w.ahead), w.unpushed)}</span></li>
        </ul>
        {ignored ? <p class="badge warn">{t.life.ignoredLost(ignored.files.join(", "))}</p> : null}
        <div class="field">
          <label for="park-note">{t.life.note}</label>
          <input id="park-note" class="input" value={note} maxLength={200} onInput={(e) => setNote((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <ErrorBanner error={error} />
        <div class="dialog-actions">
          <CopyCommand command={theoCmd("worktree", "park", project, name)} />
          <button type="button" class="btn" autoFocus onClick={onClose}>{t.app.cancel}</button>
          <button type="submit" class="btn primary" disabled={busy || w.changes > 0}>{ignored ? t.life.parkAnyway : t.life.parkConfirm}</button>
        </div>
      </form>
    </Modal>
  );
}

export function KeepDialog({ project, w, onClose, onDone }: { project: string; w: LifeWorktree; onClose: () => void; onDone: () => void }) {
  const [until, setUntil] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | undefined>();
  const name = nameOf(w);
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await api.post(`${wtPath(project, w.id)}/keep`, { until, note });
      announce(t.life.keepDone(name));
      onDone();
      onClose();
    } catch (err) {
      setError(asError(err));
      setBusy(false);
    }
  };
  return (
    <Modal title={t.life.keepTitle(name)} onClose={onClose}>
      <form onSubmit={submit} class="stack">
        <p>{t.life.keepLead}</p>
        <div class="field">
          <label for="keep-until">{t.life.keepUntil}</label>
          <input id="keep-until" class="input auto" type="date" value={until} onInput={(e) => setUntil((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <div class="field">
          <label for="keep-note">{t.life.note}</label>
          <input id="keep-note" class="input" value={note} maxLength={200} onInput={(e) => setNote((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <ErrorBanner error={error} />
        <div class="dialog-actions">
          <CopyCommand command={theoCmd("worktree", "keep", project, name, ...(until ? ["--until", until] : []))} />
          <button type="button" class="btn" autoFocus onClick={onClose}>{t.app.cancel}</button>
          <button type="submit" class="btn primary" disabled={busy}>{t.life.keepConfirm}</button>
        </div>
      </form>
    </Modal>
  );
}

export async function unkeep(project: string, w: LifeWorktree): Promise<void> {
  await api.post(`${wtPath(project, w.id)}/keep`, { clear: true });
  announce(t.life.unkeepDone(nameOf(w)));
}

export async function resumeParked(project: string, branch: string): Promise<void> {
  await api.post(`${projectPath(project)}/resume`, { branch });
  announce(t.life.resumeDone(branch));
}
