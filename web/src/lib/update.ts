// The version check and the in-app update (board V-17). One small store feeds the banner, the tag next to the search
// box and Settings › Updates. It reads GET /update when the app starts and every minute (every second while Theo
// downloads); only the buttons ask the network (POST /update/check).
import { useEffect, useState } from "preact/hooks";
import { api, ApiError } from "../api/client.ts";
import type { UpdateStatus } from "../api/types.ts";

let state: UpdateStatus | undefined;
let failure: string | undefined;
const subs = new Set<() => void>();
let timer: number | undefined;

function set(next: UpdateStatus | undefined) {
  state = next;
  failure = undefined;
  subs.forEach((fn) => fn());
}

function schedule() {
  window.clearTimeout(timer);
  if (subs.size === 0) return;
  timer = window.setTimeout(() => { void refresh(); }, state?.state === "downloading" ? 1000 : 60000);
}

export async function refresh(): Promise<void> {
  try { set(await api.get<UpdateStatus>("/update")); } catch { /* keep what we had: the banner is a convenience */ }
  schedule();
}

async function call(path: string, body?: unknown): Promise<UpdateStatus | undefined> {
  try {
    const next = await api.post<UpdateStatus>(path, body);
    set(next);
    schedule();
    return next;
  } catch (e) {
    failure = e instanceof ApiError ? e.message : String(e);
    subs.forEach((fn) => fn());
    throw e;
  }
}

export const updateActions = {
  check: () => call("/update/check"),
  install: () => call("/update/install"),
  dismiss: () => call("/update/dismiss"),
  settings: (change: { auto_check?: boolean; notify?: boolean; channel?: "stable" | "rc" }) => call("/update/settings", change),
  restart: async () => { await api.post("/update/restart"); },
};

/** The current answer, shared by every component that shows it. */
export function useUpdate(): { status: UpdateStatus | undefined; failure: string | undefined } {
  const [, force] = useState(0);
  useEffect(() => {
    const fn = () => force((n) => n + 1);
    subs.add(fn);
    if (!state) void refresh(); else schedule();
    return () => { subs.delete(fn); if (subs.size === 0) window.clearTimeout(timer); };
  }, []);
  return { status: state, failure };
}
