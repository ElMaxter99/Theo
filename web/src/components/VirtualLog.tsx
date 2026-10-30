// Virtualized log view: renders only the visible window of lines (never more than ~400 nodes, far below the
// 10 000 line budget) so 50 000+ lines stay fast. Lines are text nodes only; search matches use <mark>.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "preact/hooks";
import { LogStore, visibleRange } from "../lib/logstore.ts";
import { formatClock, splitMatches } from "../lib/format.ts";
import { t } from "../strings.ts";

export function VirtualLog({ store, version, shown, search, follow, onFollowChange, label }: {
  store: LogStore; version: number; shown: number; search: string; follow: boolean; onFollowChange: (f: boolean) => void; label: string;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [rowH, setRowH] = useState(20);
  const [range, setRange] = useState<[number, number]>([0, 0]);
  const followRef = useRef(follow);
  followRef.current = follow;

  const indices = useMemo(() => store.filter(search, shown), [store, version, shown, search]);
  const count = indices.length;

  const measure = () => {
    const el = box.current;
    if (!el) return;
    const lh = parseFloat(getComputedStyle(el).lineHeight);
    if (lh > 0 && Math.abs(lh - rowH) > 0.01) setRowH(lh);
  };

  const recompute = () => {
    const el = box.current;
    if (!el) return;
    setRange(visibleRange(el.scrollTop, el.clientHeight, rowH, count));
  };

  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    measure();
    if (followRef.current) el.scrollTop = el.scrollHeight;
    recompute();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [count, rowH, version]);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let raf = 0;
    const on = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        recompute();
        const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - rowH * 1.5;
        if (!atBottom && followRef.current) onFollowChange(false);
        else if (atBottom && !followRef.current && count > 0) onFollowChange(true);
      });
    };
    el.addEventListener("scroll", on, { passive: true });
    const ro = new ResizeObserver(() => { measure(); recompute(); });
    ro.observe(el);
    return () => { el.removeEventListener("scroll", on); ro.disconnect(); if (raf) cancelAnimationFrame(raf); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rowH, count]);

  const rows = [];
  for (let i = range[0]; i < range[1]; i++) {
    const l = store.lines[indices[i]!];
    if (!l) continue;
    const cls = "log-line" + (l.err ? " err" : "") + (l.gap ? " gap" : "");
    rows.push(
      <div key={l.id} class={cls} style={{ top: `${i * rowH}px`, height: `${rowH}px` }} data-line="1">
        {l.gap ? l.text : (
          <>
            {l.at ? <span class="ts">{formatClock(l.at)}</span> : null}
            {l.svc ? <span class="svc">{l.svc}</span> : null}
            {splitMatches(l.text, search).map((s, k) => (s.match ? <mark key={k}>{s.text}</mark> : s.text))}
          </>
        )}
      </div>,
    );
  }

  return (
    <div class="log-view" ref={box} tabIndex={0} role="region" aria-label={label}>
      <div class="log-inner" style={{ height: `${count * rowH}px` }}>{rows}</div>
    </div>
  );
}

export const logCountText = (n: number, total: number): string => t.logs.lines(n, total);
