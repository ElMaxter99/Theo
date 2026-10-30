import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import { ApiError } from "../api/client.ts";
import { events } from "../api/events.ts";

export interface Resource<T> {
  data: T | undefined;
  error: ApiError | undefined;
  loading: boolean;
  reload: () => void;
}

/**
 * Loads data with the given function and reloads it when the dependencies change or when the server says that
 * the state of the given project (and optionally worktree) changed.
 */
export function useResource<T>(load: (signal: AbortSignal) => Promise<T>, deps: unknown[], watch?: { project?: string; worktree?: string }): Resource<T> {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<ApiError | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;

  useEffect(() => {
    const ctl = new AbortController();
    let cancelled = false;
    setLoading(true);
    loadRef.current(ctl.signal).then(
      (d) => { if (!cancelled) { setData(d); setError(undefined); setLoading(false); } },
      (e) => {
        if (cancelled || (e as Error).name === "AbortError") return;
        setError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
        setLoading(false);
      },
    );
    return () => { cancelled = true; ctl.abort(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, tick]);

  const project = watch?.project;
  const worktree = watch?.worktree;
  useEffect(() => {
    if (!project) return;
    return events.on("state.changed", (d) => {
      const p = d as { project?: string; worktree?: string } | undefined;
      if (p?.project && p.project !== project) return;
      if (worktree && p?.worktree && p.worktree !== worktree) return;
      setTick((n) => n + 1);
    });
  }, [project, worktree]);

  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { data, error, loading, reload };
}

/** Runs an async action with a busy flag and an error message; for forms and buttons. */
export function useAction<A extends unknown[], R>(fn: (...args: A) => Promise<R>) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<ApiError | undefined>(undefined);
  const run = useCallback(async (...args: A): Promise<R | undefined> => {
    setBusy(true);
    setError(undefined);
    try {
      return await fn(...args);
    } catch (e) {
      setError(e instanceof ApiError ? e : new ApiError(0, { code: "internal", message: String(e) }));
      return undefined;
    } finally {
      setBusy(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fn]);
  return { run, busy, error, clear: () => setError(undefined) };
}
