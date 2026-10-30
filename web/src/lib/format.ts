// Small pure helpers (unit-tested in test/lib.test.mjs).

// Terminal colour/cursor escape sequences are stripped from log lines: the viewer shows plain text.
// eslint-disable-next-line no-control-regex
const ANSI = /\u001b\[[0-9;?]*[ -/]*[@-~]|\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)/g;

export function stripAnsi(s: string): string {
  return s.replace(ANSI, "");
}

export function formatTime(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "medium" });
}

export function formatClock(iso: string | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString(undefined, { hour12: false });
}

export function basename(p: string): string {
  const parts = p.split(/[\\/]+/).filter(Boolean);
  return parts[parts.length - 1] ?? p;
}

export function dirname(p: string): string {
  const i = Math.max(p.lastIndexOf("/"), p.lastIndexOf("\\"));
  return i > 0 ? p.slice(0, i) : p;
}

/** Folder name suggested for a branch: `feature/login` -> `feature-login`. */
export function folderForBranch(branch: string): string {
  return branch.trim().replace(/[\\/\s:*?"<>|]+/g, "-").replace(/^-+|-+$/g, "");
}

/** Suggested project name from a folder path. */
export function suggestName(path: string): string {
  return basename(path.trim()).replace(/[^A-Za-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "").toLowerCase();
}

const BAD_BRANCH = /[\s~^:?*[\\]|\.\.|@\{|\/\/|^\/|\/$|\.lock$|\.$/;
export function validBranchName(name: string): boolean {
  const n = name.trim();
  return n.length > 0 && !BAD_BRANCH.test(n) && !n.startsWith("-");
}

export function validEnvName(name: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]*$/.test(name);
}

export function parseList(s: string): string[] {
  return s.split(",").map((x) => x.trim()).filter(Boolean);
}

/** Wraps matches of `needle` (case-insensitive) as segments so the caller can render them as text nodes. */
export function splitMatches(text: string, needle: string): { text: string; match: boolean }[] {
  if (!needle) return [{ text, match: false }];
  const out: { text: string; match: boolean }[] = [];
  const lower = text.toLowerCase();
  const n = needle.toLowerCase();
  let i = 0;
  while (i < text.length) {
    const j = lower.indexOf(n, i);
    if (j < 0) { out.push({ text: text.slice(i), match: false }); break; }
    if (j > i) out.push({ text: text.slice(i, j), match: false });
    out.push({ text: text.slice(j, j + n.length), match: true });
    i = j + n.length;
  }
  return out;
}
