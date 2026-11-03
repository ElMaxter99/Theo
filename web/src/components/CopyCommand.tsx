// "Copy command": every action the interface takes has its command line (principle of the 0.2 redesign: the
// terminal stays the source of truth). Copies to the clipboard; when the browser refuses, the text is shown to select.
import { useState } from "preact/hooks";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

/** Quotes a word for a POSIX shell when it needs it (a branch or path with spaces). */
export function shq(word: string): string {
  return /^[A-Za-z0-9_./:@=+-]+$/.test(word) ? word : `'${word.replace(/'/g, `'\\''`)}'`;
}

/** `theo <args>` with each argument quoted when needed. */
export function theoCmd(...args: (string | false | null | undefined)[]): string {
  return ["theo", ...args.filter((a): a is string => typeof a === "string" && a !== "").map(shq)].join(" ");
}

export function CopyCommand({ command, label = t.copy.button, small = true }: { command: string; label?: string; small?: boolean }) {
  const [shown, setShown] = useState(false);
  const copy = async () => {
    if (await copyText(command)) { announce(t.copy.copied); setShown(false); }
    else { setShown(true); announce(t.copy.failed); }
  };
  return (
    <span class="copy-cmd">
      <button type="button" class={`btn quiet${small ? " sm" : ""}`} onClick={() => void copy()} title={command}>
        {label}<span class="sr-only">: {command}</span>
      </button>
      {shown ? <code class="copy-text" tabIndex={0}>{command}</code> : null}
    </span>
  );
}
