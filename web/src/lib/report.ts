// Text of "Copy information for a report": versions and check results only. No paths, variables or secrets.
import type { DoctorCheck, DoctorReport, Meta } from "../api/types.ts";

// A path runs to the next space, comma, semicolon, quote or parenthesis and does not end in a colon; it starts at a
// word boundary, so URLs (https://...) are left alone. Same rules as redactPaths in internal/api/redact.go.
const BODY = `(?:[^\\s,;"')]*[^\\s,;"'):])?`;
const UNC = new RegExp(`\\\\\\\\${BODY}`, "g");
const DRIVE = new RegExp(`(^|[^A-Za-z0-9])[A-Za-z]:[\\\\/]${BODY}`, "g");
const POSIX = new RegExp(`(^|[\\s"'(=,;]|://)/${BODY}`, "g");

/** Replaces the absolute paths (POSIX, Windows drive, UNC) in a line: they carry the user name. */
export function redactPaths(s: string): string {
  return s.replace(UNC, "<path>").replace(DRIVE, "$1<path>").replace(POSIX, "$1<path>");
}

export function reportText(meta: Meta | undefined, doctor: DoctorReport | undefined): string {
  const lines = [
    `Theo ${meta?.theo_version ?? "unknown"} (API ${meta?.api_version ?? "unknown"})`,
    `Platform: ${doctor?.platform ?? meta?.os ?? "unknown"}${meta?.arch ? "/" + meta.arch : ""}`,
    `Docker: ${meta?.docker.available ? meta.docker.version ?? "available" : "not available"}`,
    `Git: ${meta?.git.available ? meta.git.version ?? "available" : "not available"}`,
    "",
    "Checks:",
    ...(doctor?.checks ?? []).map((c: DoctorCheck) => `- [${c.status}] ${c.name}${c.detail ? ": " + redactPaths(c.detail) : ""}`),
  ];
  return lines.join("\n");
}
