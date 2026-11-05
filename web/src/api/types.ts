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
  /** Root-relative patterns copied from the main worktree into a new one (`project set --copy-on-add`). */
  copy_on_add?: string[];
  /** Whether the app stores its own address in its data (Q-020): absent means auto (by image). */
  url_in_data?: "on" | "off";
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
  /** url-in-data (Q-020): the address the data was made for, the current one, the service, and the app's own command. */
  previous?: string;
  current?: string;
  service?: string;
  command?: string;
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

/** What the coding agent of a worktree is doing (internal/agents). source "process" means estimated. */
export interface AgentState {
  state: "working" | "waiting" | "done";
  source: "hook" | "process";
  agent?: string;
  session?: string;
  since: string;
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
  /** Owner label (an agent's name), set with --owner or `theo worktree owner`. */
  owner?: string;
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
  /** Local domains of the instance, only while a proxy runs. */
  proxy_urls?: ProxyLink[];
  error?: string;
  busy?: Busy | null;
  agent?: AgentState;
}

/** An address the proxy serves a service on (proxy.Link). */
export interface ProxyLink {
  service: string;
  url: string;
  /** The same address over HTTPS, while the proxy serves it. */
  https_url?: string;
  /** The short form <instance>.<project>, for the web, app, api or frontend service. */
  default?: boolean;
  running: boolean;
}

/** GET /proxy. */
export interface ProxyStatus {
  running: boolean;
  /** The interface's own proxy, which it can stop. */
  managed: boolean;
  port: number;
  url?: string;
  pid?: number;
  autostart: boolean;
  error?: string;
  /** The running proxy also serves HTTPS (or, while off, the switch asks for it). */
  https: boolean;
  https_port: number;
  /** Theo's local certificate authority, once it exists (ADR-0015). */
  authority?: ProxyAuthority;
}

export interface ProxyAuthority {
  /** The certificate, the file to import in Firefox. */
  path: string;
  created: string;
  fingerprint: string;
  trust: { system: boolean; nss?: "trusted" | "not-trusted" | "unknown" };
  trusted: boolean;
  /** Windows and macOS: the interface can trust it itself. Elsewhere it shows `command`. */
  can_trust_here: boolean;
  command: string;
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
  /** The profiles the Compose files declare (0.2). */
  known_profiles?: string[];
  /** The worktree's owner label (0.2). */
  owner?: string;
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

// ---------- 0.2 additions (docs/api.md) ----------

/** GET /status: the same JSON as `theo status --json` (only what the screens use). */
export interface StatusReport {
  projects: StatusProject[];
  docker_error?: string;
  warnings: WtWarning[];
}

export interface StatusProject {
  name: string;
  path: string;
  error?: string;
  worktrees: StatusWorktree[];
  warnings: WtWarning[];
}

export interface StatusWorktree {
  id: string;
  path: string;
  branch?: string;
  kind: string;
  dirty: boolean;
  missing?: boolean;
  instance?: {
    compose_project?: string;
    instance_state: InstanceState;
    counts: { running: number; total: number };
    ports: WtPort[];
    proxy_urls?: string[];
  };
  warnings: WtWarning[];
  /** Where the workspace is in its life (ADR-0016); absent for the main worktree. */
  lifecycle?: Lifecycle;
  agent?: AgentState;
}

/** GET|POST /projects/{p}/prune. */
export interface PruneReport {
  project: string;
  into: string;
  applied: boolean;
  merged: { path: string; branch: string; owner?: string; reason?: string; removed: boolean }[];
  not_merged: number;
}

/** GET .../volumes/backups. */
export interface BackupInfo {
  project: string;
  instance: string;
  volume: string;
  docker_volume: string;
  archive: string;
  size: number;
  created_at: string;
  file: string;
}

/** GET /mcp/install. */
export interface McpInstall {
  name: string;
  command: string;
  args: string[];
  claude_code: string[];
  claude_code_command: string;
  mcpServers: Record<string, { command: string; args: string[] }>;
  snippet: string;
}

export interface RenameResult {
  old: string;
  new: string;
  records: number;
  worktree_dir?: string;
  notes?: string[];
}

/** Lifecycle of a workspace (ADR-0016). */
export type LifeState = "missing" | "in-use" | "kept" | "merged" | "inactive" | "unpushed" | "new" | "pushed";

export interface Lifecycle {
  state?: LifeState;
  activity_at?: string;
  activity_source?: "commit" | "files" | "mcp" | "instance" | "created";
  idle_days: number;
  base?: string;
  base_recorded?: boolean;
  ahead: number;
  behind: number;
  upstream?: string;
  upstream_gone?: boolean;
  unpushed: number;
  merged_by?: "ancestor" | "content";
  changes: number;
  running?: boolean;
  origin?: string;
  created_at?: string;
  last_seen?: string;
  kept?: boolean;
  kept_until?: string;
  keep_note?: string;
  size_bytes?: number;
  reason?: string;
  problem?: string;
}

/** One workspace of GET /projects/{p}/lifecycle. */
export interface LifeWorktree extends Lifecycle {
  id: string;
  path: string;
  branch?: string;
  head?: string;
  detached?: boolean;
  locked?: boolean;
  prunable?: boolean;
  kind: string;
  owner?: string;
  dirty: boolean;
}

export interface ParkedWorktree {
  branch: string;
  path: string;
  base?: string;
  owner?: string;
  note?: string;
  parked_at: string;
  branch_gone?: boolean;
}

/** GET /projects/{p}/lifecycle. */
export interface LifeReport {
  project: string;
  inactive_after_days: number;
  worktrees: LifeWorktree[];
  parked: ParkedWorktree[];
  notes: string[];
}

export interface CleanEntry {
  path: string;
  branch?: string;
  owner?: string;
  state: LifeState;
  merged_by?: "ancestor" | "content";
  idle_days: number;
  note?: string;
  reason?: string;
  changes?: number;
  unpushed?: number;
  upstream_gone?: boolean;
  closed: boolean;
  went_down?: boolean;
  branch_deleted?: boolean;
  images_removed?: string[];
  problems?: string[];
}

/** GET|POST /projects/{p}/clean. */
export interface CleanReport {
  project: string;
  applied: boolean;
  inactive_after_days: number;
  close: CleanEntry[];
  inactive: CleanEntry[];
  kept: CleanEntry[];
  leftovers: { id: string; kind: string; subject: string; detail: string; fix?: string; forgettable?: boolean }[];
  forgot?: number;
  notes?: string[];
}

export interface ParkInactiveEntry {
  path: string;
  branch?: string;
  idle_days: number;
  changes: number;
  unpushed: number;
  upstream_gone?: boolean;
  ignored?: number;
  reason?: string;
  will_save?: boolean;
  saved?: boolean;
  parked: boolean;
}

/** GET|POST /projects/{p}/park-inactive. */
export interface ParkInactiveReport {
  project: string;
  applied: boolean;
  entries: ParkInactiveEntry[];
  notes?: string[];
}
