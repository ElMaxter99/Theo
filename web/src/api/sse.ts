// Server-Sent Events over fetch: a small, testable parser plus a reconnecting stream with Last-Event-ID.
// fetch (not EventSource) lets us see HTTP status codes, send Last-Event-ID ourselves and abort cleanly.
import { BASE } from "./client.ts";

export interface SseEvent {
  event: string;
  id?: string;
  data: string;
}

/** Incremental parser of the text/event-stream format. */
export class SseParser {
  private buf = "";
  private ev = "";
  private id: string | undefined;
  private data: string[] = [];

  push(chunk: string): SseEvent[] {
    this.buf += chunk;
    const out: SseEvent[] = [];
    let start = 0;
    for (;;) {
      const m = /\r\n|\n|\r/.exec(this.buf.slice(start));
      if (!m) break;
      // A lone \r at the very end may be the first half of \r\n: wait for more data.
      if (m[0] === "\r" && start + m.index + 1 === this.buf.length) break;
      const line = this.buf.slice(start, start + m.index);
      start += m.index + m[0].length;
      this.line(line, out);
    }
    this.buf = this.buf.slice(start);
    return out;
  }

  private line(line: string, out: SseEvent[]) {
    if (line === "") {
      if (this.data.length > 0 || this.ev) out.push({ event: this.ev || "message", id: this.id, data: this.data.join("\n") });
      this.ev = "";
      this.data = [];
      return;
    }
    if (line.startsWith(":")) return; // comment / keep-alive
    const i = line.indexOf(":");
    const field = i < 0 ? line : line.slice(0, i);
    let value = i < 0 ? "" : line.slice(i + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "event") this.ev = value;
    else if (field === "data") this.data.push(value);
    else if (field === "id") this.id = value;
  }
}

export type StreamState = "connecting" | "open" | "reconnecting" | "closed";

export interface StreamOptions {
  /** Path below /api/v1, with query string. */
  path: string;
  onEvent: (e: SseEvent) => void;
  onState?: (s: StreamState, detail?: { status?: number; message?: string }) => void;
  /** Reconnect when the connection drops (default true). */
  reconnect?: boolean;
}

export interface Stream {
  close(): void;
}

let openCount = 0;
export const openStreams = (): number => openCount;

const FATAL = new Set([400, 401, 403, 404, 405, 409, 412, 422]);

export function openStream(opts: StreamOptions): Stream {
  const ctl = new AbortController();
  let closed = false;
  let lastId: string | undefined;
  let attempt = 0;

  const set = (s: StreamState, d?: { status?: number; message?: string }) => { if (!closed || s === "closed") opts.onState?.(s, d); };

  async function run() {
    while (!closed) {
      set(attempt === 0 ? "connecting" : "reconnecting");
      let counted = false;
      try {
        const headers: Record<string, string> = { Accept: "text/event-stream" };
        if (lastId !== undefined) headers["Last-Event-ID"] = lastId;
        const res = await fetch(BASE + opts.path, { headers, credentials: "same-origin", cache: "no-store", signal: ctl.signal });
        if (!res.ok || !res.body) {
          if (FATAL.has(res.status) || opts.reconnect === false) {
            closed = true;
            set("closed", { status: res.status, message: `HTTP ${res.status}` });
            return;
          }
          throw new Error(`HTTP ${res.status}`);
        }
        openCount++;
        counted = true;
        attempt = 0;
        set("open");
        const reader = res.body.getReader();
        const dec = new TextDecoder();
        const parser = new SseParser();
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          for (const ev of parser.push(dec.decode(value, { stream: true }))) {
            if (ev.id !== undefined) lastId = ev.id;
            opts.onEvent(ev);
          }
        }
      } catch (e) {
        if (closed || (e as Error).name === "AbortError") return;
      } finally {
        if (counted) openCount--;
      }
      if (closed) return;
      if (opts.reconnect === false) {
        closed = true;
        set("closed");
        return;
      }
      attempt++;
      const wait = Math.min(10000, 500 * 2 ** Math.min(attempt, 5));
      await new Promise((r) => setTimeout(r, wait));
    }
  }
  void run();

  return {
    close() {
      if (closed) return;
      closed = true;
      ctl.abort();
      opts.onState?.("closed");
    },
  };
}
