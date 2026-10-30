// Native <dialog> wrappers. Esc cancels (native), focus returns to the opener, the destructive variant never
// focuses the destructive button first and never contains the mascot.
import { useEffect, useId, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { ApiError } from "../api/client.ts";
import { t } from "../strings.ts";
import { ErrorBanner } from "./ui.tsx";
import { Icon } from "./Icon.tsx";

export function Modal({ title, onClose, children, danger, wide }: { title: string; onClose: () => void; children: ComponentChildren; danger?: boolean; wide?: boolean }) {
  const ref = useRef<HTMLDialogElement>(null);
  const id = useId();
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const opener = document.activeElement as HTMLElement | null;
    if (!d.open) d.showModal();
    const onCloseEvt = () => closeRef.current();
    d.addEventListener("close", onCloseEvt);
    return () => {
      d.removeEventListener("close", onCloseEvt);
      if (d.open) d.close();
      if (opener && document.contains(opener)) opener.focus();
    };
  }, []);

  return (
    <dialog ref={ref} aria-labelledby={id} class={danger ? "danger" : undefined} data-wide={wide ? "1" : undefined}>
      <div class="dialog-body">
        <h2 id={id}>{danger ? <><Icon name="warn" size={20} /> </> : null}{title}</h2>
        {children}
      </div>
    </dialog>
  );
}

/** Simple confirmation for reversible or non-destructive actions (level 0-1). */
export function ConfirmDialog({ title, body, confirmLabel, onConfirm, onClose, children }: {
  title: string; body?: ComponentChildren; confirmLabel: string; onConfirm: () => Promise<unknown>; onClose: () => void; children?: ComponentChildren;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | undefined>();
  const submit = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await onConfirm();
      onClose();
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
      setBusy(false);
    }
  };
  return (
    <Modal title={title} onClose={onClose}>
      {body ? <p>{body}</p> : null}
      {children}
      <ErrorBanner error={error} />
      <div class="dialog-actions">
        <button type="button" class="btn" autoFocus onClick={onClose}>{t.app.cancel}</button>
        <button type="button" class="btn danger" disabled={busy} onClick={submit}>{confirmLabel}</button>
      </div>
    </Modal>
  );
}

export interface TypedConfirmProps {
  title: string;
  lead?: ComponentChildren;
  expected: string;
  prompt?: string;
  affected?: { kind?: string; name: string; detail?: string }[];
  preserved?: string[];
  confirmLabel: string;
  /** Headings of the two lists; the defaults fit deletions ("What will be deleted"). */
  affectedTitle?: string;
  preservedTitle?: string;
  /** Sends the confirmation; throws ApiError when the server rejects it. */
  onConfirm: (text: string) => Promise<unknown>;
  onClose: () => void;
}

/**
 * Level-3 confirmation (plan section 3): lists exactly what is deleted and what is kept, and enables the button only
 * when the typed text matches. The server validates again; its rejection is shown and nothing is deleted.
 */
export function TypedConfirmDialog(p: TypedConfirmProps) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | undefined>();
  const inputId = useId();
  const match = text === p.expected;
  const submit = async (e: Event) => {
    e.preventDefault();
    if (!match) return;
    setBusy(true);
    setError(undefined);
    try {
      await p.onConfirm(text);
      p.onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err : new ApiError(0, { code: "internal", message: String(err) }));
      setBusy(false);
    }
  };
  return (
    <Modal title={p.title} onClose={p.onClose} danger>
      <form onSubmit={submit}>
        {p.lead ? <p>{p.lead}</p> : null}
        {p.affected && p.affected.length > 0 ? (
          <>
            <h3>{p.affectedTitle ?? t.confirm.willDelete}</h3>
            <ul class="affected" aria-label={p.affectedTitle ?? t.confirm.willDelete}>
              {p.affected.map((a, i) => (
                <li key={i}><strong class="mono">{a.name}</strong>{a.detail ? <span class="muted"> — {a.detail}</span> : null}</li>
              ))}
            </ul>
          </>
        ) : null}
        {p.preserved && p.preserved.length > 0 ? (
          <>
            <h3>{p.preservedTitle ?? t.confirm.preserved}</h3>
            <ul>{p.preserved.map((x, i) => <li key={i}>{x}</li>)}</ul>
          </>
        ) : null}
        <div class="field">
          <label for={inputId}>{p.prompt ?? t.confirm.typeToConfirm(p.expected)}</label>
          <p class="hint" id={`${inputId}-h`}>{t.confirm.typeToConfirm(p.expected)}</p>
          <input id={inputId} class="input mono" value={text} autocomplete="off" autocapitalize="off" spellcheck={false} aria-describedby={`${inputId}-h`}
            onInput={(e) => setText((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <ErrorBanner error={error} title={t.confirm.serverRejected} />
        <p class="hint">{t.confirm.cancelDefault}</p>
        <div class="dialog-actions">
          <button type="button" class="btn" autoFocus onClick={p.onClose}>{t.app.cancel}</button>
          <button type="submit" class="btn danger solid" disabled={!match || busy}>{p.confirmLabel}</button>
        </div>
      </form>
    </Modal>
  );
}
