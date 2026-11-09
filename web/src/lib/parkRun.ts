// The bulk park whose progress window is open. It lives outside the cleanup list because parking makes the list
// shrink (and the list may disappear), and the window with the result must outlive it.
import { useEffect, useState } from "preact/hooks";
import type { ParkInactiveRun } from "../api/types.ts";

/** `saves` maps a worktree path to its uncommitted files (saved first), `options` are what the run was started with. */
export interface ParkRunRef { project: string; opId: string; saves?: Record<string, number>; options?: { include_ignored: boolean; delete_branches: boolean } }

let current: ParkRunRef | null = null;
const listeners = new Set<(r: ParkRunRef | null) => void>();

export function showParkRun(r: ParkRunRef | null): void {
  current = r;
  for (const l of listeners) l(r);
}

export function useParkRun(): ParkRunRef | null {
  const [r, setR] = useState<ParkRunRef | null>(current);
  useEffect(() => {
    listeners.add(setR);
    setR(current);
    return () => { listeners.delete(setR); };
  }, []);
  return r;
}

// What the last finished run left, for the result step of the cleanup page. It outlives the progress window.
export interface ParkResult {
  project: string;
  run: ParkInactiveRun;
  /** The options it ran with, so a failed one can be tried again. */
  options: { include_ignored: boolean; delete_branches: boolean };
}
let result: ParkResult | null = null;
const resultListeners = new Set<(r: ParkResult | null) => void>();

export function setParkResult(r: ParkResult | null): void {
  result = r;
  for (const l of resultListeners) l(r);
}

export function useParkResult(): ParkResult | null {
  const [r, setR] = useState<ParkResult | null>(result);
  useEffect(() => {
    resultListeners.add(setR);
    setR(result);
    return () => { resultListeners.delete(setR); };
  }, []);
  return r;
}
