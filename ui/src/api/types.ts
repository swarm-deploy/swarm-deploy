export interface SyncInfo {
  last_sync_at?: string;
  last_sync_reason?: string;
  last_sync_result?: string;
  git_revision?: string;
  last_sync_error?: string;
  [key: string]: string | undefined;
}

export interface StackView {
  name: string;
  compose_file: string;
  last_status: string;
  last_error?: string;
  last_commit?: string;
  last_deploy_at?: string;
  source_digest?: string;
}

export interface StacksResponse {
  stacks: StackView[];
  sync: SyncInfo;
}

export interface GitCommitDetailsResponse {
  full_hash: string;
  author: string;
  date: string;
  changed_files: string[];
}

export type ServiceType = "application" | "monitoring" | "delivery" | "reverseProxy" | "database";

export interface WebRoute {
  domain: string;
  address: string;
  port: string;
}

export interface ServiceInfo {
  name: string;
  stack: string;
  type: ServiceType;
  type_title: string;
  image: string;
  image_version: string;
  description?: string;
  repository_url?: string;
  web_routes?: WebRoute[];
}

export interface ServicesResponse {
  services: ServiceInfo[];
}

export interface QueueResponse {
  queued: boolean;
}

export interface CurrentUserResponse {
  name: string;
}

export type EventSeverity = "info" | "warn" | "error" | "alert";
export type EventCategory = "sync" | "security";

export interface EventHistoryItem {
  type: string;
  severity: EventSeverity;
  category: EventCategory;
  created_at: string;
  message: string;
  details?: Record<string, string>;
}

export interface EventHistoryResponse {
  events: EventHistoryItem[];
}

export interface ServiceSpecSecretResponse {
  secret_id?: string;
  secret_name: string;
  target?: string;
}

export interface ServiceSpecNetworkResponse {
  target: string;
  aliases?: string[];
}

export interface ServiceSpecLabelsResponse {
  docker?: Record<string, string>;
  custom?: Record<string, string>;
}

export interface ServiceSpecResponse {
  image: string;
  mode: string;
  replicas: number;
  requested_ram_bytes: number;
  requested_cpu_nano: number;
  limit_ram_bytes: number;
  limit_cpu_nano: number;
  labels?: ServiceSpecLabelsResponse;
  secrets?: ServiceSpecSecretResponse[];
  network?: ServiceSpecNetworkResponse[];
}

export interface ServiceStatusResponse {
  stack: string;
  service: string;
  spec: ServiceSpecResponse;
}

export type ServiceDeploymentStatus = "success" | "failed";

export interface ServiceDeploymentResponse {
  created_at: string;
  status: ServiceDeploymentStatus;
  image: string;
  image_version: string;
  message?: string;
  commit?: string;
}

export interface ServiceDeploymentsResponse {
  deployments: ServiceDeploymentResponse[];
}

export interface NodeInfo {
  id: string;
  hostname: string;
  status: string;
  availability: string;
  manager_status: string;
  engine_version: string;
  addr: string;
}

export interface NodesResponse {
  nodes: NodeInfo[];
}

export interface NetworkInfo {
  id: string;
  name: string;
  scope: string;
  driver: string;
  internal: boolean;
  attachable: boolean;
  ingress: boolean;
  labels?: Record<string, string>;
  options?: Record<string, string>;
}

export interface NetworksResponse {
  networks: NetworkInfo[];
}

export interface SecretExternalInfo {
  path?: string;
  version_id?: string;
}

export interface SecretInfo {
  id: string;
  name: string;
  version_id: number;
  created_at: string;
  external?: SecretExternalInfo;
}

export interface SecretsResponse {
  secrets: SecretInfo[];
}

export interface SecretDetailsResponse {
  id: string;
  name: string;
  version_id: number;
  created_at: string;
  updated_at: string;
  driver?: string;
  labels?: Record<string, string>;
  external?: SecretExternalInfo;
}

export type SearchResultKind = "service" | "secret" | "stack";
export type SearchResultMatch = "service_name" | "service_web_route" | "secret_name" | "stack_name";

export interface SearchResult {
  kind: SearchResultKind;
  match: SearchResultMatch;
  label: string;
  stack?: string;
  service?: string;
  secret_name?: string;
}

export interface SearchResponse {
  results: SearchResult[];
}

export type AssistantStatus = "in_progress" | "completed" | "failed" | "rejected" | "disabled";

export interface AssistantChatRequest {
  conversation_id?: string;
  request_id?: string;
  message?: string;
  wait_timeout_ms?: number;
}

export interface AssistantChatResponse {
  status: AssistantStatus;
  request_id: string;
  conversation_id: string;
  answer?: string;
  error_message?: string;
  poll_after_ms?: number;
}
