// SSE budget: HTTP/1.1 allows 6 connections per origin, and the contract promises at most TWO streams per tab:
// the global event channel (events.ts) and ONE "focus" stream (a log view or an operation output).
// Whoever claims the focus slot preempts the previous owner, which is told so it can show "paused".
let current: { id: symbol; onPreempt: () => void } | null = null;

export function claimFocusStream(onPreempt: () => void): { release: () => void; id: symbol } {
  const id = Symbol("focus-stream");
  if (current) current.onPreempt();
  current = { id, onPreempt };
  return {
    id,
    release() {
      if (current && current.id === id) current = null;
    },
  };
}
