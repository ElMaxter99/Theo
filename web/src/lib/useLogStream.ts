// Connects a LogStore to an SSE stream (service logs or operation output) under the "max 2 streams per tab" rule.
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { openStream } from "../api/sse.ts";
import type { SseEvent, StreamState } from "../api/sse.ts";
import { claimFocusStream } from "../api/focus.ts";
import { LogStore } from "./logstore.ts";
import { t } from "../strings.ts";

export type LogStatus = "idle" | "connecting" | "live" | "reconnecting" | "ended" | "error" | "preempted";

export interface UseLogStreamOptions {
  /** Path below /api/v1 (with query). Empty or `enabled=false` closes the stream. */
  path: string;
  enabled: boolean;
  /** Called for events other than `log` and `gap` (state, result...). */
  onOther?: (ev: SseEvent, data: unknown) => void;
  /** Reconnecting after the tab was hidden or preempted must not duplicate history: ask only for new lines. */
  resumePath?: string;
}

interface LogPayload { service?: string; line?: string; at?: string; stream?: string; truncated?: boolean }

export function useLogStream(opts: UseLogStreamOptions) {
  const storeRef = useRef<LogStore>(new LogStore());
  const [version, setVersion] = useState(0);
  const [status, setStatus] = useState<LogStatus>("idle");
  const [paused, setPaused] = useState(false);
  const [frozen, setFrozen] = useState(0);
  const [epoch, setEpoch] = useState(0);
  const dirty = useRef(false);
  const onOther = useRef(opts.onOther);
  onOther.current = opts.onOther;
  const pausedRef = useRef(false);
  pausedRef.current = paused;
  const resumed = useRef(false);

  // Flush buffered lines to the UI at most every 100 ms.
  useEffect(() => {
    const id = window.setInterval(() => {
      if (dirty.current) { dirty.current = false; setVersion((v) => v + 1); }
    }, 100);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    if (!opts.enabled || !opts.path) { setStatus("idle"); return; }
    const store = storeRef.current;
    const claim = claimFocusStream(() => { stream.close(); setStatus("preempted"); });
    const path = resumed.current && opts.resumePath ? opts.resumePath : opts.path;
    const stream = openStream({
      path,
      reconnect: true,
      onEvent: (ev) => {
        let data: unknown;
        try { data = ev.data ? JSON.parse(ev.data) : undefined; } catch { data = undefined; }
        if (ev.event === "log") {
          const d = (data ?? {}) as LogPayload;
          store.push({ at: d.at, svc: d.service, text: (d.line ?? "") + (d.truncated ? ` ${t.logs.truncated}` : ""), err: d.stream === "stderr" });
          dirty.current = true;
        } else if (ev.event === "gap") {
          const n = (data as { dropped?: number } | undefined)?.dropped;
          store.push({ text: n ? t.logs.gap(n) : t.logs.gapUnknown, gap: true });
          dirty.current = true;
        } else if (ev.event === "end" || ev.event === "result") {
          // The server finished this stream: stop here instead of reconnecting (a reconnect would replay the tail).
          onOther.current?.(ev, data);
          stream.close();
          setStatus("ended");
        } else onOther.current?.(ev, data);
      },
      onState: (s: StreamState, detail) => {
        if (s === "open") setStatus("live");
        else if (s === "connecting") setStatus("connecting");
        else if (s === "reconnecting") setStatus("reconnecting");
        else if (s === "closed") setStatus((cur) => (cur === "preempted" ? cur : detail?.status ? "error" : "ended"));
      },
    });
    // Hidden tabs do not keep a log stream open; it resumes (without history) when the tab is visible again.
    const onVis = () => {
      if (document.visibilityState === "hidden") { stream.close(); setStatus("preempted"); }
      else if (document.visibilityState === "visible") { resumed.current = true; setEpoch((n) => n + 1); }
    };
    document.addEventListener("visibilitychange", onVis);
    return () => { document.removeEventListener("visibilitychange", onVis); stream.close(); claim.release(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opts.path, opts.enabled, epoch]);

  const togglePause = useCallback(() => {
    setPaused((p) => {
      if (!p) setFrozen(storeRef.current.lines.length);
      return !p;
    });
  }, []);

  const clear = useCallback(() => {
    storeRef.current.clear();
    setFrozen(0);
    setVersion((v) => v + 1);
  }, []);

  /** Re-opens the stream after it was preempted by another view. */
  const resume = useCallback(() => { resumed.current = true; setEpoch((n) => n + 1); }, []);
  const reset = useCallback(() => { resumed.current = false; storeRef.current.clear(); setFrozen(0); setVersion((v) => v + 1); }, []);

  const store = storeRef.current;
  const shown = paused ? frozen : store.lines.length;
  return { store, version, shown, status, paused, togglePause, clear, resume, reset };
}
