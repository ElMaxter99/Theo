// Menu button (WAI-ARIA APG "menu button" pattern). Replaces the <details> menus of 0.1: Enter, Space or ArrowDown
// open it on the first item, ArrowUp on the last; arrows, Home and End move; Escape and Tab close it and focus goes
// back to the button; a click outside closes it.
// The list is placed with position: fixed next to the button, so a table or card that scrolls or clips its content
// (overflow) cannot cut it; it opens upwards when there is no room below, stays inside the window and follows the
// button when something scrolls.
import { useEffect, useId, useLayoutEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";

export interface MenuItem {
  label: string;
  /** Link target: the item is a link. */
  href?: string;
  onSelect?: () => void;
  danger?: boolean;
  disabled?: boolean;
  /** Text after the label only for screen readers (the instance the action is for). */
  sr?: string;
}

export function MenuButton({ label, items, children, class: cls = "btn sm", testid }: {
  label: string; items: (MenuItem | null | false | undefined)[]; children?: ComponentChildren; class?: string; testid?: string;
}) {
  const list = items.filter((x): x is MenuItem => !!x);
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const [pos, setPos] = useState<{ top: number; left: number; maxHeight: number } | null>(null);
  const btn = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const id = useId();

  const focusItem = (i: number) => {
    const els = menu.current?.querySelectorAll<HTMLElement>("[role=menuitem]");
    if (!els || els.length === 0) return;
    const n = (i + els.length) % els.length;
    setActive(n);
    els[n]?.focus();
  };
  const show = (at: number) => { setOpen(true); window.setTimeout(() => focusItem(at < 0 ? list.length - 1 : at), 0); };
  const close = (refocus = true) => { setOpen(false); if (refocus) btn.current?.focus(); };

  // Place the list against the button: below and right-aligned by default, above when it does not fit below.
  const place = () => {
    const b = btn.current?.getBoundingClientRect();
    const m = menu.current;
    if (!b || !m) return;
    const vh = window.innerHeight;
    if (b.bottom < 0 || b.top > vh) { setOpen(false); return; } // the button scrolled out of sight
    const gap = 4;
    const margin = 8;
    const vw = document.documentElement.clientWidth;
    const w = m.offsetWidth;
    const h = m.scrollHeight;
    const below = vh - b.bottom - gap - margin;
    const above = b.top - gap - margin;
    const up = h > below && above > below;
    const room = Math.max(up ? above : below, 120);
    const height = Math.min(h, room);
    const top = up ? b.top - gap - height : b.bottom + gap;
    const left = Math.min(Math.max(margin, b.right - w), Math.max(margin, vw - w - margin));
    setPos((p) => (p && p.top === top && p.left === left && p.maxHeight === room ? p : { top, left, maxHeight: room }));
  };
  useLayoutEffect(() => {
    if (!open) { setPos(null); return; }
    place();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, list.length]);

  // Follow the button when the page or a table scrolls, or the window changes size.
  useEffect(() => {
    if (!open) return;
    const onMove = (e: Event) => {
      if (menu.current && e.target instanceof Node && menu.current.contains(e.target)) return;
      place();
    };
    window.addEventListener("scroll", onMove, true);
    window.addEventListener("resize", onMove);
    return () => { window.removeEventListener("scroll", onMove, true); window.removeEventListener("resize", onMove); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const target = e.target as Node;
      if (!menu.current?.contains(target) && !btn.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [open]);

  const onButtonKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown" || e.key === "Enter" || e.key === " ") { e.preventDefault(); show(0); }
    else if (e.key === "ArrowUp") { e.preventDefault(); show(-1); }
  };
  const onMenuKey = (e: KeyboardEvent) => {
    if (e.key === "ArrowDown") { e.preventDefault(); focusItem(active + 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); focusItem(active - 1); }
    else if (e.key === "Home") { e.preventDefault(); focusItem(0); }
    else if (e.key === "End") { e.preventDefault(); focusItem(list.length - 1); }
    else if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); close(); }
    else if (e.key === "Tab") { setOpen(false); }
  };
  const pick = (it: MenuItem) => {
    if (it.disabled) return;
    close(!it.href);
    it.onSelect?.();
  };

  return (
    <div class="menu">
      <button ref={btn} type="button" class={cls} aria-haspopup="menu" aria-expanded={open} aria-controls={open ? id : undefined}
        aria-label={label} data-testid={testid} onClick={() => (open ? close() : show(0))} onKeyDown={onButtonKey}>
        {children ?? <span aria-hidden="true">⋯</span>}
      </button>
      {open ? (
        <div ref={menu} id={id} role="menu" aria-label={label} class="menu-list" onKeyDown={onMenuKey}
          style={pos ? { top: `${pos.top}px`, left: `${pos.left}px`, maxHeight: `${pos.maxHeight}px` } : { visibility: "hidden" }}>
          {list.map((it, i) => it.href && !it.disabled ? (
            <a key={i} role="menuitem" tabIndex={-1} href={it.href} class={`menu-item${it.danger ? " danger" : ""}`} onClick={() => pick(it)}>
              {it.label}{it.sr ? <span class="sr-only"> {it.sr}</span> : null}
            </a>
          ) : (
            <button key={i} type="button" role="menuitem" tabIndex={-1} aria-disabled={it.disabled || undefined} class={`menu-item${it.danger ? " danger" : ""}`}
              onClick={() => pick(it)}>
              {it.label}{it.sr ? <span class="sr-only"> {it.sr}</span> : null}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
