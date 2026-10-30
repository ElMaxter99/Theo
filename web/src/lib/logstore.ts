// In-memory line store for the log viewer. Only a window of it is ever rendered (see VirtualLog).
import { stripAnsi } from "./format.ts";

export interface LogLine {
  id: number;
  at?: string;
  svc?: string;
  text: string;
  err?: boolean;
  gap?: boolean;
}

export const MAX_STORED = 100_000;
const DROP_CHUNK = 10_000;

export class LogStore {
  lines: LogLine[] = [];
  private nextId = 1;
  /** Total lines ever pushed (not only the ones still stored). */
  total = 0;

  push(l: Omit<LogLine, "id">): void {
    this.lines.push({ ...l, id: this.nextId++, text: stripAnsi(l.text) });
    this.total++;
    if (this.lines.length > MAX_STORED) this.lines.splice(0, DROP_CHUNK);
  }

  clear(): void {
    this.lines = [];
  }

  /** Indices of the lines that contain `needle` (all of them when empty), limited to the first `limit` lines. */
  filter(needle: string, limit: number): number[] {
    const n = Math.min(limit, this.lines.length);
    const out: number[] = [];
    if (!needle) {
      for (let i = 0; i < n; i++) out.push(i);
      return out;
    }
    const q = needle.toLowerCase();
    for (let i = 0; i < n; i++) {
      const l = this.lines[i]!;
      if (l.gap || l.text.toLowerCase().includes(q) || (l.svc ?? "").toLowerCase().includes(q)) out.push(i);
    }
    return out;
  }

  toText(): string {
    return this.lines.map((l) => `${l.at ? l.at + " " : ""}${l.svc ? l.svc + " | " : ""}${l.text}`).join("\n");
  }
}

/** Window of rows to render for a scroll position (fixed row height). Never more than `max` rows. */
export function visibleRange(scrollTop: number, viewport: number, rowHeight: number, count: number, overscan = 20, max = 400): [number, number] {
  if (count === 0 || rowHeight <= 0) return [0, 0];
  let start = Math.max(0, Math.floor(scrollTop / rowHeight) - overscan);
  let end = Math.min(count, Math.ceil((scrollTop + viewport) / rowHeight) + overscan);
  if (end - start > max) end = start + max;
  if (start > count) start = count;
  return [start, end];
}
