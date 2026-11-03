// Command palette (Ctrl K or ⌘K, or "/" outside a text field): go to any screen, project or instance, or run a
// common action, by typing part of its name. A combobox with a listbox (WAI-ARIA APG): arrows move, Enter runs,
// Escape closes and focus goes back where it was.
import { useEffect, useId, useMemo, useRef, useState } from "preact/hooks";
import { href, navigate } from "../lib/router.ts";
import { statusName, useStatus, wtState } from "../lib/statusStore.ts";
import { stopAll } from "../ops/store.ts";
import { announce } from "../lib/live.ts";
import { t } from "../strings.ts";

interface Entry {
  id: string;
  label: string;
  group: string;
  hint?: string;
  run: () => void;
}

export function Palette({ onClose }: { onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const [q, setQ] = useState("");
  const [active, setActive] = useState(0);
  const status = useStatus();
  const listId = useId();
  const closeRef = useRef(onClose);
  closeRef.current = onClose;

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const opener = document.activeElement as HTMLElement | null;
    if (!d.open) d.showModal();
    input.current?.focus();
    const onCloseEvt = () => closeRef.current();
    d.addEventListener("close", onCloseEvt);
    return () => {
      d.removeEventListener("close", onCloseEvt);
      if (d.open) d.close();
      if (opener && document.contains(opener)) opener.focus();
    };
  }, []);

  const entries = useMemo<Entry[]>(() => {
    const go = (to: string) => () => navigate(to);
    const p = t.palette;
    const out: Entry[] = [
      { id: "home", label: t.nav.home, group: p.groupGo, hint: "g i", run: go(href.home()) },
      { id: "projects", label: t.nav.projects, group: p.groupGo, hint: "g p", run: go(href.projects()) },
      { id: "problems", label: t.nav.problems, group: p.groupGo, hint: "g x", run: go(href.problems()) },
      { id: "cleanup", label: t.problems.cleanupTab, group: p.groupGo, run: go(href.problems("cleanup")) },
      { id: "settings", label: t.nav.settings, group: p.groupGo, run: go(href.settings()) },
      { id: "agents", label: t.settingsNav.agents, group: p.groupGo, run: go(href.settings("agents")) },
      { id: "domains", label: t.settingsNav.domains, group: p.groupGo, run: go(href.settings("domains")) },
      { id: "add", label: t.projects.add, group: p.groupActions, run: go(href.add()) },
      { id: "stopall", label: t.home.stopAll, group: p.groupActions, run: () => { void stopAll().then(() => announce(t.home.stoppingAll), () => undefined); } },
    ];
    for (const pr of status.report?.projects ?? []) {
      out.push({ id: `p:${pr.name}`, label: pr.name, group: p.groupProjects, hint: p.project, run: go(href.project(pr.name)) });
      out.push({ id: `n:${pr.name}`, label: p.newIn(pr.name), group: p.groupActions, run: go(href.wizard(pr.name)) });
      for (const w of pr.worktrees) {
        if (w.kind === "bare" || w.missing) continue;
        out.push({ id: `w:${w.id}`, label: `${pr.name} › ${statusName(w)}`, group: p.groupInstances, hint: t.state[wtState(w)] ?? wtState(w), run: go(href.detail(pr.name, w.id)) });
      }
    }
    return out;
  }, [status.report]);

  const shown = useMemo(() => {
    const words = q.toLowerCase().split(/\s+/).filter(Boolean);
    const list = words.length === 0 ? entries : entries.filter((e) => words.every((w) => e.label.toLowerCase().includes(w) || e.group.toLowerCase().includes(w)));
    return list.slice(0, 50);
  }, [q, entries]);

  useEffect(() => setActive(0), [q]);

  const run = (e: Entry | undefined) => {
    if (!e) return;
    ref.current?.close();
    e.run();
  };
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); setActive((a) => Math.min(a + 1, shown.length - 1)); }
    else if (e.key === "ArrowUp") { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)); }
    else if (e.key === "Enter") { e.preventDefault(); run(shown[active]); }
  };
  useEffect(() => {
    document.getElementById(`${listId}-${active}`)?.scrollIntoView({ block: "nearest" });
  }, [active, listId]);

  return (
    <dialog ref={ref} class="palette" aria-label={t.palette.title}>
      <div class="palette-body">
        <label class="sr-only" for={`${listId}-q`}>{t.palette.title}</label>
        <input id={`${listId}-q`} ref={input} class="input" type="text" role="combobox" aria-expanded="true" aria-controls={listId} aria-autocomplete="list"
          aria-activedescendant={shown[active] ? `${listId}-${active}` : undefined} placeholder={t.palette.placeholder} value={q} autocomplete="off" spellcheck={false}
          onInput={(e) => setQ((e.currentTarget as HTMLInputElement).value)} onKeyDown={onKey} />
        <ul id={listId} role="listbox" class="palette-list" aria-label={t.palette.results}>
          {shown.map((e, i) => (
            <li key={e.id} id={`${listId}-${i}`} role="option" aria-selected={i === active} class="palette-item"
              onMouseMove={() => setActive(i)} onClick={() => run(e)}>
              <span>{e.label}</span>
              <span class="small muted">{e.hint ?? e.group}</span>
            </li>
          ))}
        </ul>
        {shown.length === 0 ? <p class="muted small" role="status">{t.palette.none}</p> : null}
        <p class="hint">{t.palette.keys}</p>
      </div>
    </dialog>
  );
}

/** The keyboard shortcuts of the whole page. "g" then a letter goes somewhere; "?" lists them. */
export function useShortcuts(open: () => void, help: () => void): void {
  useEffect(() => {
    let pending = false;
    let timer: number | undefined;
    const typing = (el: EventTarget | null) => {
      const n = el as HTMLElement | null;
      return !!n && (n.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(n.tagName));
    };
    const on = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") { e.preventDefault(); open(); return; }
      if (e.ctrlKey || e.metaKey || e.altKey || typing(e.target) || document.querySelector("dialog[open]")) return;
      if (pending) {
        pending = false;
        clearTimeout(timer);
        const to = e.key === "i" ? href.home() : e.key === "p" ? href.projects() : e.key === "x" ? href.problems() : e.key === "s" ? href.settings() : "";
        if (to) { e.preventDefault(); navigate(to); }
        return;
      }
      if (e.key === "/") { e.preventDefault(); open(); }
      else if (e.key === "?") { e.preventDefault(); help(); }
      else if (e.key === "g") { pending = true; timer = window.setTimeout(() => { pending = false; }, 1200); }
    };
    document.addEventListener("keydown", on);
    return () => { document.removeEventListener("keydown", on); clearTimeout(timer); };
  }, [open, help]);
}
