// Shapes of the API (gui-contract.md, reconciled with docs/api.md of the backend track).
// Unknown fields are ignored; optional fields are marked with `?`.

export type InstanceState = "running" | "partial" | "unhealthy" | "stopped" | "absent" | "unknown";

export interface Meta {
  theo_version: string;
  api_version: string;
  os?: string;
  arch?: string;
  docker: { available: boolean; version?: string; error?: string; hint?: string };
  git: { available: boolean; version?: string; error?: string };
  data_dir?: string;
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
  containers?: { running: number; total: number } | Container[];
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
