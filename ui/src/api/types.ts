export interface SyncInfo {
  last_sync_at?: string;
  last_sync_reason?: string;
  last_sync_result?: string;
  git_revision?: string;
  last_sync_error?: string;
  [key: string]: string | undefined;
}

export interface ServiceView {
  name: string;
  image: string;
  image_version: string;
  last_status?: string;
  last_deploy_at?: string;
}

export interface StackView {
  name: string;
  compose_file: string;
  last_status: string;
  last_error?: string;
  last_commit?: string;
  last_deploy_at?: string;
  source_digest?: string;
  services: ServiceView[];
}

export interface StacksResponse {
  stacks: StackView[];
  sync: SyncInfo;
}

export interface QueueResponse {
  queued: boolean;
}

export interface EventHistoryItem {
  type: string;
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

export interface ServiceSpecResponse {
  image: string;
  mode: string;
  replicas: number;
  requested_ram_bytes: number;
  requested_cpu_nano: number;
  limit_ram_bytes: number;
  limit_cpu_nano: number;
  labels?: Record<string, string>;
  secrets?: ServiceSpecSecretResponse[];
  network?: ServiceSpecNetworkResponse[];
}

export interface ServiceStatusResponse {
  stack: string;
  service: string;
  spec: ServiceSpecResponse;
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
