// Counts the requests in flight so the header can show that the app is working (a thin progress bar).
import { useEffect, useState } from "preact/hooks";

let pending = 0;
const subs = new Set<() => void>();
const notify = () => subs.forEach((fn) => fn());

/** Marks one request as started; call the returned function when it ends (success, error or abort). */
export function beginRequest(): () => void {
  pending++;
  notify();
  let done = false;
  return () => { if (!done) { done = true; pending--; notify(); } };
}

export function useBusy(): boolean {
  const [n, setN] = useState(pending);
  useEffect(() => {
    const fn = () => setN(pending);
    subs.add(fn);
    fn();
    return () => { subs.delete(fn); };
  }, []);
  return n > 0;
}
