/** The JSON of the backend (internal/… in Go); field names match the Go json tags. */

export type Access = "ok" | "unavailable" | "forbidden";

export interface Status {
  docker: Access;
  version?: string;
  apiVersion?: string;
  os?: string;
  arch?: string;
  /** CPUs of the Docker host. */
  cpus?: number;
  /** Memory of the Docker host in bytes. */
  memory?: number;
  admin: boolean;
  message?: string;
}

export interface Ref {
  id: string;
  name: string;
}

export interface PublishedPort {
  hostIp?: string;
  hostPort?: number;
  containerPort: number;
  protocol: string;
}

export interface ContainerSummary {
  id: string;
  name: string;
  image: string;
  imageId: string;
  state: string;
  status: string;
  created: number;
  ports: PublishedPort[];
  networks: string[];
}

export interface Port {
  hostIp: string;
  /** 0: Docker picks the port. A host port range the container already has also shows as 0 (and stays on save). */
  hostPort: number;
  containerPort: number;
  protocol: "tcp" | "udp";
}

export interface Mount {
  type: "volume" | "bind" | string;
  source: string;
  target: string;
  readOnly: boolean;
  anonymous?: boolean;
}

export interface EnvVar {
  name: string;
  value: string;
}

/** A label of the container itself: Docker's own (com.docker.*, ...) and those of the image are not in the form. */
export interface Label {
  key: string;
  value: string;
}

export type Policy = "no" | "always" | "unless-stopped" | "on-failure";

export interface Restart {
  policy: Policy;
  maxRetries: number;
}

export interface Resources {
  /** Memory limit in bytes; 0: no limit. */
  memory: number;
  /** CPU limit in cores (1.5); 0: no limit. */
  cpus: number;
}

export interface Spec {
  name: string;
  image: string;
  command: string;
  ports: Port[];
  mounts: Mount[];
  env: EnvVar[];
  labels: Label[];
  network: string;
  restart: Restart;
  privileged: boolean;
  resources: Resources;
  start?: boolean;
}

export interface ContainerState {
  status: string;
  running: boolean;
  exitCode: number;
  startedAt: string;
  finishedAt: string;
  error?: string;
  health?: string;
}

export interface Endpoint {
  name: string;
  ipv4?: string;
  gateway?: string;
  aliases: string[];
}

export interface ContainerDetail {
  id: string;
  name: string;
  image: string;
  imageId: string;
  created: string;
  state: ContainerState;
  command: string;
  ports: PublishedPort[];
  mounts: Mount[];
  networks: Endpoint[];
  env: string[];
  restart: Restart;
  tty: boolean;
}

export interface ContainerInfo {
  container: ContainerDetail;
  spec: Spec;
}

export interface CreateResult {
  id: string;
  warnings?: string[];
  warning?: string;
}

export interface EditResult {
  id: string;
  recreated: boolean;
  /** Docker's warnings about the new configuration (limits the kernel cannot enforce, …). */
  warnings?: string[];
  warning?: string;
}

/** Data of the event "line" of GET /containers/:id/logs (which also sends "end" and "error"). */
export interface LogLine {
  stream: "stdout" | "stderr";
  time: string;
  line: string;
}

/** Data of the event "stats" of GET /containers/:id/stats (which also sends "stopped" and "error"). */
export interface StatsSample {
  time: string;
  cpuPercent: number;
  memUsage: number | null;
  memLimit: number | null;
  netRx: number;
  netTx: number;
  blkRead: number;
  blkWrite: number;
  pids: number;
}

/** Data of an event "error" of a stream (logs, statistics, pulls): the stream failed in the middle. */
export interface StreamError {
  message: string;
}

export interface ImageInfo {
  id: string;
  tags: string[];
  size: number;
  created: number;
  usedBy: Ref[];
}

export interface PullStart {
  id: string;
  ref: string;
}

export interface Layer {
  id: string;
  status: string;
  current: number;
  total: number;
}

export interface PullProgress {
  ref: string;
  status: string;
  layers: Layer[];
  done: boolean;
  error?: string;
}

export interface Volume {
  name: string;
  driver: string;
  mountpoint: string;
  createdAt: string;
  scope: string;
  options: Record<string, string>;
  anonymous: boolean;
  usedBy: Ref[];
}

export interface NetworkInfo {
  id: string;
  name: string;
  driver: string;
  scope: string;
  subnet?: string;
  gateway?: string;
  internal: boolean;
  predefined: boolean;
  containers: number;
}

export interface NetworkDetail extends NetworkInfo {
  options: Record<string, string>;
  labels: Record<string, string>;
  attached: { id: string; name: string; ipv4?: string }[];
}

export interface Registry {
  id: string;
  name: string;
  address: string;
  username?: string;
  hasPassword: boolean;
}

export interface RegistryList {
  registries: Registry[];
  writable: boolean;
}

export interface RegistryInput {
  name: string;
  address: string;
  auth: boolean;
  username: string;
  password: string;
}

export interface TestResult {
  ok: boolean;
  message: string;
}

export const DOCKER_HUB = "docker.io";

/** The 12-character form of an ID, as docker ps shows it. */
export function shortId(id: string): string {
  return id.replace(/^sha256:/, "").slice(0, 12);
}
