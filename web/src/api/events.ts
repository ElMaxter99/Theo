// The global event channel (GET /events): one SSE connection per tab. Everything else subscribes here.
import { openStream } from "./sse.ts";
import { api } from "./client.ts";
import type { Stream, SseEvent, StreamState } from "./sse.ts";

type Listener = (data: unknown, raw: SseEvent) => void;

class Events {
  private listeners = new Map<string, Set<Listener>>();
  private stream: Stream | null = null;
  state: StreamState = "closed";
  private wasOpen = false;
  private stateListeners = new Set<(s: StreamState) => void>();

  on(type: string, fn: Listener): () => void {
    let set = this.listeners.get(type);
    if (!set) this.listeners.set(type, (set = new Set()));
    set.add(fn);
    return () => set.delete(fn);
  }

  onState(fn: (s: StreamState) => void): () => void {
    this.stateListeners.add(fn);
    return () => this.stateListeners.delete(fn);
  }

  private emit(type: string, data: unknown, raw: SseEvent) {
    for (const key of [type, type.split(".")[0] + ".*", "*"]) this.listeners.get(key)?.forEach((fn) => fn(data, raw));
  }

  start(): void {
    if (this.stream) return;
    this.stream = openStream({
      path: "/events",
      onEvent: (ev) => {
        let data: unknown;
        try { data = ev.data ? JSON.parse(ev.data) : undefined; } catch { data = undefined; }
        this.emit(ev.event, data, ev);
      },
      onState: (s) => {
        this.state = s;
        this.stateListeners.forEach((fn) => fn(s));
        const first = s === "open" && !this.wasOpen;
        if (s === "open") this.wasOpen = true;
        if (first) return;
        // After a reconnect we may have missed events: tell everyone to refresh.
        if (s === "open" && this.wasOpen) this.emit("state.changed", {}, { event: "state.changed", data: "" });
      },
    });
  }

  stop(): void {
    this.stream?.close();
    this.stream = null;
  }

  /** Tells the server whether the tab is visible so that it polls Docker only then (contract 5.3). */
  focus(visible: boolean): void {
    void api.post("/events/focus", { visible }).catch(() => undefined);
  }
}

export const events = new Events();
