// HTTP client for /api/v1: cookie session + CSRF header, typed errors, Idempotency-Key for operations.
// No secret is ever put in a URL: the launch nonce lives in the URL fragment only until it is exchanged.
import type { ApiErrorBody } from "./types.ts";

export const BASE = "/api/v1";

export class ApiError extends Error {
  status: number;
  body: ApiErrorBody;
  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.name = "ApiError";
    this.status = status;
    this.body = body;
  }
  get code(): string {
    return this.body.code;
  }
}

let csrf = "";
const lostListeners = new Set<(reason: SessionProblem) => void>();
export type SessionProblem = "no-session" | "bad-nonce" | "server-gone";

export function onSessionLost(fn: (reason: SessionProblem) => void): () => void {
  lostListeners.add(fn);
  return () => lostListeners.delete(fn);
}
function sessionLost(reason: SessionProblem) {
  for (const fn of lostListeners) fn(reason);
}

function storeCsrf(v: string) {
  csrf = v;
  try { sessionStorage.setItem("theo.csrf", v); } catch { /* storage may be unavailable: the token is then re-read from the server */ }
}
function loadCsrf(): string {
  try { return sessionStorage.getItem("theo.csrf") ?? ""; } catch { return ""; }
}

export function uuid(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c && typeof c.randomUUID === "function") return c.randomUUID();
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (ch) => {
    const r = (Math.random() * 16) | 0;
    return (ch === "x" ? r : (r & 3) | 8).toString(16);
  });
}

function pickCsrf(body: unknown): string {
  if (body && typeof body === "object") {
    const b = body as Record<string, unknown>;
    for (const k of ["csrf", "csrf_token", "csrfToken", "token"]) if (typeof b[k] === "string") return b[k] as string;
  }
  return "";
}

async function parseError(res: Response): Promise<ApiError> {
  let body: ApiErrorBody | undefined;
  try {
    const j = await res.json();
    if (j && typeof j === "object" && j.error && typeof j.error === "object") body = j.error as ApiErrorBody;
  } catch { /* not JSON */ }
  return new ApiError(res.status, body ?? { code: `http_${res.status}`, message: res.statusText || `HTTP ${res.status}` });
}

export interface RequestOptions {
  idempotent?: boolean;
  signal?: AbortSignal;
}

async function request<T>(method: string, path: string, body?: unknown, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method !== "GET") {
    headers["X-Theo-Csrf"] = csrf;
    headers["Content-Type"] = "application/json";
    if (opts.idempotent) headers["Idempotency-Key"] = uuid();
  }
  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      body: method === "GET" || body === undefined ? undefined : JSON.stringify(body),
      credentials: "same-origin",
      cache: "no-store",
      referrerPolicy: "no-referrer",
      signal: opts.signal,
    });
  } catch (e) {
    if ((e as Error).name === "AbortError") throw e;
    sessionLost("server-gone");
    throw new ApiError(0, { code: "network", message: "Theo is not reachable." });
  }
  if (res.status === 401) {
    sessionLost("no-session");
    throw await parseError(res);
  }
  if (!res.ok) throw await parseError(res);
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get("content-type") ?? "";
  if (ct.includes("application/json")) return (await res.json()) as T;
  return (await res.text()) as unknown as T;
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>("GET", path, undefined, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("POST", path, body ?? {}, opts),
  put: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("PUT", path, body ?? {}, opts),
  patch: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("PATCH", path, body ?? {}, opts),
  del: <T>(path: string, opts?: RequestOptions) => request<T>("DELETE", path, undefined, opts),
};

export const qs = (params: Record<string, string | number | boolean | undefined | string[]>): string => {
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "" || v === false) continue;
    if (Array.isArray(v)) v.forEach((x) => u.append(k, x));
    else u.set(k, v === true ? "1" : String(v));
  }
  const s = u.toString();
  return s ? `?${s}` : "";
};

export const projectPath = (p: string) => `/projects/${encodeURIComponent(p)}`;
export const wtPath = (p: string, wt: string) => `${projectPath(p)}/worktrees/${encodeURIComponent(wt)}`;

/**
 * Establishes the session. The launch URL looks like http://127.0.0.1:PORT/#n=<nonce>; the nonce is read from the
 * fragment (never sent to the server by the browser), exchanged once for a cookie and a CSRF token, and then
 * removed from the address bar so that no URL contains a secret.
 */
export async function startSession(): Promise<"ok" | SessionProblem> {
  const m = /(?:^|[#&])n=([A-Za-z0-9_\-.~]+)/.exec(location.hash);
  if (m) {
    const nonce = m[1]!;
    history.replaceState(null, "", location.pathname + location.search);
    try {
      const res = await fetch(`${BASE}/session`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ nonce }),
        credentials: "same-origin",
        cache: "no-store",
        referrerPolicy: "no-referrer",
      });
      if (!res.ok) return res.status === 401 || res.status === 403 ? "bad-nonce" : "server-gone";
      const body = await res.json().catch(() => ({}));
      storeCsrf(pickCsrf(body) || res.headers.get("X-Theo-Csrf") || "");
      return "ok";
    } catch {
      return "server-gone";
    }
  }
  csrf = loadCsrf();
  try {
    const res = await fetch(`${BASE}/meta`, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } });
    if (res.status === 401) return "no-session";
    if (!res.ok) return "server-gone";
    if (!csrf) {
      // The cookie is valid but this tab lost the CSRF token (for example a new tab): ask the server for it.
      const s = await fetch(`${BASE}/session`, { credentials: "same-origin", cache: "no-store", headers: { Accept: "application/json" } }).catch(() => null);
      if (s && s.ok) storeCsrf(pickCsrf(await s.json().catch(() => ({}))) || s.headers.get("X-Theo-Csrf") || "");
    }
    return "ok";
  } catch {
    return "server-gone";
  }
}
