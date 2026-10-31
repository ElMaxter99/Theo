// Text of "Copy information for a report": versions and check results only. No paths, variables or secrets.
import type { DoctorCheck, DoctorReport, Meta } from "../api/types.ts";

export function reportText(meta: Meta | undefined, doctor: DoctorReport | undefined): string {
  const lines = [
    `Theo ${meta?.theo_version ?? "unknown"} (API ${meta?.api_version ?? "unknown"})`,
    `Platform: ${doctor?.platform ?? meta?.os ?? "unknown"}${meta?.arch ? "/" + meta.arch : ""}`,
    `Docker: ${meta?.docker.available ? meta.docker.version ?? "available" : "not available"}`,
    `Git: ${meta?.git.available ? meta.git.version ?? "available" : "not available"}`,
    "",
    "Checks:",
    ...(doctor?.checks ?? []).map((c: DoctorCheck) => `- [${c.status}] ${c.name}${c.detail ? ": " + c.detail : ""}`),
  ];
  return lines.join("\n");
}

