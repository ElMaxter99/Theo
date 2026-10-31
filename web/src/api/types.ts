// Shapes of the API (gui-contract.md, reconciled with docs/api.md of the backend track).
// Unknown fields are ignored; optional fields are marked with `?`.

export type InstanceState = "running" | "partial" | "unhealthy" | "stopped" | "absent" | "unknown";

export interface Meta {
  theo_version: string;
  api_version: string;
  os?: string;
  arch?: string;
  docker: ToolInfo & { hint?: string };
  compose?: ToolInfo;
  git: ToolInfo;
  data_dir?: string;
  idle?: { timeout_seconds: number };
}

export interface ToolInfo {
  available: boolean;
  version?: string;
  /** The oldest version Theo works with (Compose, Git). */
  min_version?: string;
  error?: string;
}

export interface Project {
  name: string;
  path: string;
  env_file?: string;
  worktree_dir?: string;
  worktree_dir_effective?: string;
  compose_files?: string[];
  added_at: string;
}

export interface EnvFileInfo {
  path?: string;
  source: string;
}

export interface Busy {
  operation_id: string;
  kind: string;
}

/** A host port of a worktree as the overview reports it (engine.PortStatus). */
export interface WtPort {
  service: string;
  container_port: number;
  protocol?: string;
  host_port?: number;
  url?: string;
  status: string;
}

export interface WtWarning {
  kind: string;
  message: string;
  fix?: string;
}

export interface UrlInfo {
  service: string;
  url: string;
  port?: number;
}

export interface PortConflict {
  service: string;
  port: number;
  detail?: string;
}

export interface Worktree {
  id: string;
  path: string;
  branch?: string;
  head?: string;
  detached?: boolean;
  locked?: boolean;
  prunable?: boolean;
  main?: boolean;
  kind?: string;
  dirty?: boolean;
  compose_project?: string;
  env_file?: EnvFileInfo;
  state: InstanceState;
  /** List responses carry counts; the detail response carries the containers themselves. */
  containers?: { running: number; total: number } | RawContainer[];
  /** Detail responses carry the counts here. */
  container_counts?: { running: number; total: number };
  ports?: WtPort[];
  warnings?: WtWarning[];
  error?: string;
  busy?: Busy | null;
}

export interface Overview {
  project: string;
  path: string;
  docker: { available: boolean; error?: string };
  worktrees: Worktree[];
  generated_at?: string;
}

export interface Branch {
  name: string;
  remote?: boolean;
  worktree?: string;
}

export interface Publisher {
  url?: string;
  target_port: number;
  published_port: number;
  protocol?: string;
}

/** A container as the detail endpoint returns it: the raw `docker compose ps` record (capitalised keys). */
export interface RawContainer {
  Name?: string;
  Service?: string;
  State?: string;
  Health?: string;
  Status?: string;
  ExitCode?: number;
  Publishers?: { URL?: string; TargetPort?: number; PublishedPort?: number; Protocol?: string }[];
}

/** The same container with the names the interface uses. */
export interface Container {
  name: string;
  service: string;
  state: string;
  health?: string;
  status?: string;
  exit_code?: number;
  publishers?: Publisher[];
}

export interface InstanceSettings {
  neutralize_container_names?: boolean;
  auto_ports?: boolean;
  profiles?: string[];
}

export interface WorktreeDetail extends Worktree {
  settings?: InstanceSettings;
}

export interface Mount {
  type?: string;
  source?: string;
  target?: string;
  read_only?: boolean;
  outside_workspace?: boolean;
}

export interface ServiceInfo {
  name: string;
  image?: string;
  build?: string;
  ports?: { host_ip?: string; published?: string; target?: number; protocol?: string }[];
  mounts?: Mount[];
  depends_on?: string[];
  networks?: string[];
  profiles?: string[];
}

export interface Services {
  env_file?: EnvFileInfo;
  services: ServiceInfo[];
}

export type PortStatus = "free" | "running" | "in use" | "reassigned" | "kept" | "random" | "range";

export interface PortMapping {
  service: string;
  target: number;
  protocol?: string;
  host_ip?: string;
  configured?: string;
  effective?: number;
  running?: number;
  status: PortStatus | string;
  url?: string;
}

export interface PortPlan {
  ports: PortMapping[];
}

export interface EnvVar {
  name: string;
  value: string;
  secret?: boolean;
  services?: string[];
}

export interface VolumeInfo {
  name: string;
  volume?: string;
  status: string;
  containers?: string[];
  created?: boolean;
}

export interface HistoryEvent {
  time: string;
  user?: string;
  project?: string;
  workspace?: string;
  compose_project?: string;
  operation: string;
  result: string;
  error?: string;
}

export type OperationState = "queued" | "running" | "succeeded" | "failed" | "cancelling" | "cancelled";

export interface Operation {
  id: string;
  kind: string;
  project?: string;
  worktree?: string;
  state: OperationState;
  started_at?: string;
  finished_at?: string;
  result?: { ok?: boolean; summary?: string } | null;
  error?: string | { code?: string; message?: string };
  cancellable?: boolean;
  label?: string;
}

export interface AddedWorktree {
  path: string;
  branch: string;
  id?: string;
  env_file?: EnvFileInfo;
  notes?: string[];
}

export interface BrowseEntry {
  name: string;
  path: string;
  is_repo?: boolean;
}

export interface Browse {
  path: string;
  parent?: string;
  home?: string;
  is_repo?: boolean;
  truncated?: boolean;
  entries: BrowseEntry[];
}

export interface ApiErrorBody {
  code: string;
  message: string;
  hint?: string;
  level?: number;
  resource?: Record<string, string>;
  original?: string;
  confirm?: { expected: string; prompt?: string };
  affected?: { kind: string; name: string; detail?: string }[];
  preserved?: string[];
  request_id?: string;
}

export interface SessionInfo {
  csrf_token: string;
  api_version?: string;
}

// ---- Shared services (V5), reconcile and doctor (V6). Shapes follow engine.SharedStatus, engine.Report, engine.DoctorReport.

export interface SharedService {
  name: string;
  image?: string;
  /** running, exited, ... or "not created" */
  state: string;
}

export interface SharedConsumer {
  workspace: string;
  branch?: string;
  compose_project?: string;
  services: string[];
  /** The instance has running containers. */
  active: boolean;
}

export interface SharedStatus {
  project: string;
  compose_project: string;
  from: string;
  network: string;
  publish_ports: boolean;
  services: SharedService[];
  consumers: SharedConsumer[];
  warnings?: string[];
}

export interface Finding {
  /** Stable id if the server sends one; otherwise the interface derives it (see lib/reconcile.ts). */
  id?: string;
  kind: string;
  project?: string;
  subject: string;
  worktree?: string;
  detail: string;
  fix?: string;
  forgettable?: boolean;
}

export interface ReconcileReport {
  findings: Finding[];
  docker_error?: string;
}

export interface DoctorCheck {
  id: string;
  name: string;
  status: "ok" | "warn" | "fail" | "info" | string;
  detail?: string;
  fix?: string;
}

export interface DoctorReport {
  ok: boolean;
  platform: string;
  data_dir: string;
  checks: DoctorCheck[];
}

/** GET /diagnostics: everything the settings screen shows. */
export interface Diagnostics {
  meta: Meta;
  doctor: DoctorReport;
  projects: number;
  /** Plain text that is safe to paste into an issue (no secrets; it does include the data directory). */
  report?: string;
}

/** GET /health: what the indicator of the shell shows. */
export interface HealthSummary {
  status: "ok" | "attention" | "docker_unavailable" | string;
  findings: number;
  forgettable?: number;
  docker_error?: string;
}

/** POST /reconcile/apply */
export interface ReconcileApplied {
  forgotten: number;
  applied: string[];
  skipped: string[];
}

/** GET .../{wt}/settings */
export interface WorktreeSettingsInfo extends InstanceSettings {
  shared?: string[];
  join?: string[];
  /** service -> container_name for the services that set one. */
  fixed_container_names?: Record<string, string>;
}
