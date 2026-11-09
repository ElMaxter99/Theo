// The bulk park whose progress window is open. It lives outside the cleanup list because parking makes the list
// shrink (and the list may disappear), and the window with the result must outlive it.
import { useEffect, useState } from "preact/hooks";

export interface ParkRunRef { project: string; opId: string }

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
