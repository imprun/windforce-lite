export type Settings = {
  workspace: string;
  token: string;
  actor: string;
};

export const defaultSettings: Settings = {
  workspace: "default",
  token: "",
  actor: "local-dev",
};

export function loadSettings(): Settings {
  const store = globalThis.localStorage;
  if (!store) return defaultSettings;
  // `??` keeps a deliberately cleared value ("") cleared across reloads;
  // only a missing key falls back to the default.
  return {
    workspace: store.getItem("wf.workspace") || defaultSettings.workspace,
    token: store.getItem("wf.token") ?? defaultSettings.token,
    actor: store.getItem("wf.actor") ?? defaultSettings.actor,
  };
}

export function saveSettings(settings: Settings) {
  const store = globalThis.localStorage;
  if (!store) return;
  store.setItem("wf.workspace", settings.workspace);
  store.setItem("wf.token", settings.token);
  store.setItem("wf.actor", settings.actor);
}

export type GitSource = {
  id: number;
  workspace_id: string;
  name: string;
  repo_url: string;
  branch: string;
  subpath: string;
  creds_ref: string;
  kind: string;
  last_synced_commit?: string | null;
  last_synced_at?: string | null;
  created_at: string;
};

export type Client = {
  id: string;
  workspace_id: string;
  name: string;
  has_token: boolean;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
};

export type ClientPayload = {
  name: string;
};

export type ClientTokenResult = {
  client: Client;
  api_token: string;
};

export type InputConfig = {
  workspace_id: string;
  app_key: string;
  action_key: string;
  client_id?: string;
  config: Record<string, unknown>;
  locked_keys: string[];
  updated_by: string;
  updated_at: string;
};

export type InputConfigPayload = {
  action_key: string;
  client_id?: string;
  config: Record<string, unknown>;
  locked_keys: string[];
};

export type InputConfigAudit = {
  id: string;
  workspace_id: string;
  app_key: string;
  action_key: string;
  client_id?: string;
  kind: string;
  detail?: string;
  actor: string;
  created_at: string;
};

export type ProbeResult = {
  reachable: boolean;
  branch?: string;
  branch_exists?: boolean;
  branches?: string[];
  error?: string;
};

export type SourceSyncResult = {
  commit: string;
  app: string;
  actions: string[];
  runtime: string;
  sync_status: "synced";
  synced_at: string;
  validation_checks: string[];
};

export type DeployResult = {
  commit: string;
  app: string;
  actions: string[];
  source?: string;
  deployment_id?: string;
  created_by?: string;
  message?: string;
  bundle_status: "ready";
  bundle_digest: string;
  bundle_uri?: string;
  runtime: string;
  validation_checks: string[];
};

export type AppSummary = {
  id: string;
  workspace_id: string;
  app_key: string;
  git_source_id: number;
  commit_sha: string;
  entrypoint: string;
  tag: string;
  tag_override?: string;
  timeout_s: number;
  script_lang: string;
  bundle_status: "ready" | "missing";
  bundle_digest?: string;
  bundle_uri?: string;
  required_capabilities?: string[];
  max_concurrent?: number | null;
  updated_at: string;
  effective_route_tag: string;
  actions_count: number;
};

export type ActionView = {
  id: string;
  workspace_id: string;
  app_key: string;
  action_key: string;
  display_name?: string;
  input_schema?: string;
  output_schema?: string;
  tag?: string;
  tag_override?: string;
  timeout_s?: number;
  required_capabilities?: string[];
  updated_at: string;
  effective_capabilities?: string[];
  effective_route_tag?: string;
};

export type AppDetail = {
  app: AppSummary;
  actions: ActionView[];
};

export type ActionSchemas = {
  workspace_id: string;
  app_key: string;
  action_key: string;
  input_schema: unknown;
  output_schema: unknown;
  operator_settings_schema: unknown;
};

export type AppDocumentation = {
  app_key: string;
  commit_sha: string;
  available: boolean;
  path?: string;
  markdown?: string;
};

export type HistoryItem = {
  id: string;
  commit_sha: string;
  entrypoint: string;
  source: string;
  active: boolean;
  bundle_status: "ready" | "missing";
  deployment_id?: string;
  message?: string;
  created_by?: string;
  created_at: string;
};

export type ReleaseRollbackResult = {
  app: string;
  active_release_id: string;
  previous_release_id: string;
  commit: string;
  bundle_digest: string;
  actor: string;
  reason: string;
  rolled_back_at: string;
};

export type JobStatusCounts = {
  queued_count: number;
  running_count: number;
  completed_count_recent: number;
  failed_count_recent: number;
  canceled_count_recent: number;
};

export type JobsSummary = JobStatusCounts & {
  oldest_queued_at?: string | null;
  by_tag?: Array<JobStatusCounts & { tag: string }>;
  by_app?: Array<JobStatusCounts & { app_key: string }>;
};

export type AuditRecord = {
  id: string;
  git_source_id: number;
  app_key?: string;
  kind: string;
  detail?: string;
  actor: string;
  created_at: string;
};

export type AuditChanges = {
  added?: string[];
  updated?: string[];
  removed?: string[];
  locked?: string[];
  unlocked?: string[];
};

export type AuditEvent = {
  id: string;
  category: "repository" | "release" | "client" | "input_settings" | string;
  kind: string;
  summary: string;
  detail?: string;
  app_key?: string;
  action_key?: string;
  client_id?: string;
  client_name?: string;
  git_source_id?: number;
  webhook_subscription_id?: string;
  webhook_delivery_id?: string;
  actor: string;
  changes?: AuditChanges;
  created_at: string;
};

export type WebhookSubscription = {
  id: string;
  workspace_id: string;
  name: string;
  endpoint_summary: string;
  has_signing_secret: boolean;
  event_types: string[] | null;
  app_keys: string[] | null;
  enabled: boolean;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

export type WebhookSubscriptionMutation = {
  subscription: WebhookSubscription;
  signing_secret?: string;
};

export type WebhookSubscriptionCreate = {
  name: string;
  endpoint: string;
  event_types?: string[];
  app_keys?: string[];
  enabled?: boolean;
};

export type WebhookSubscriptionUpdate = {
  name?: string;
  endpoint?: string;
  event_types?: string[];
  app_keys?: string[];
  enabled?: boolean;
  rotate_signing_secret?: boolean;
};

export function webhookAppKeys(subscription: WebhookSubscription): string[] {
  return subscription.app_keys || [];
}

export type ControlPlaneEvent = {
  specversion: string;
  id: string;
  type: string;
  source: string;
  subject: string;
  time: string;
  datacontenttype: string;
  data: Record<string, unknown>;
};

export type WebhookDeliveryState =
  | "pending"
  | "delivering"
  | "retrying"
  | "succeeded"
  | "failed"
  | "canceled";

export type WebhookDelivery = {
  id: string;
  workspace_id: string;
  event_id: string;
  subscription_id: string;
  state: WebhookDeliveryState;
  attempt: number;
  next_attempt_at: string;
  lease_owner?: string | null;
  lease_expires_at?: string | null;
  response_status?: number | null;
  latency_ms?: number | null;
  error_summary?: string | null;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type WebhookDeliveryDetail = {
  delivery: WebhookDelivery;
  event: ControlPlaneEvent;
  subscription_name: string;
};

export type WebhookDeliveryPage = {
  items: WebhookDeliveryDetail[];
  next_cursor?: string;
};

export type ProvisioningAppliedResource = {
  kind: string;
  name: string;
  action: string;
  detail?: string;
};

export type ProvisioningImportResult = {
  applied: ProvisioningAppliedResource[];
};

export type SystemInfo = {
  service: string;
  workspace: string;
  ready: boolean;
  planes: Record<string, boolean>;
  backends: Record<string, boolean>;
  auth: Record<string, boolean>;
  runtime_config: Record<string, unknown>;
};

export type Workspace = {
  id: string;
  name: string;
  status: "active" | "archived";
  has_token: boolean;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
};

export type WorkspaceAudit = {
  id: string;
  workspace_id: string;
  kind: string;
  detail?: string;
  actor: string;
  created_at: string;
};

export type WorkspaceTokenResult = {
  workspace: Workspace;
  api_token: string;
};

export type WebhookDeliveryQuery = {
  state?: WebhookDeliveryState | "";
  limit?: number;
  cursor?: string;
};

export type AuditEventQuery = {
  appKey?: string;
  clientID?: string;
  category?: string;
  actor?: string;
  gitSourceID?: number;
  since?: string;
  until?: string;
  limit?: number;
};

export type RegisterSourcePayload = {
  name: string;
  repo_url: string;
  branch?: string;
  subpath?: string;
  creds_ref?: string;
};

export type SetVariablePayload = {
  path: string;
  value: string;
  description?: string;
  is_secret?: boolean;
  app_key?: string;
};

export type PatchSourcePayload = {
  name?: string;
  repo_url?: string;
  branch?: string;
  subpath?: string;
  creds_ref?: string;
};

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

export function errorMessage(cause: unknown): string {
  if (cause instanceof ApiError && cause.status === 401) {
    return "Unauthorized — check the API token in Settings.";
  }
  if (cause instanceof Error) return cause.message;
  return String(cause);
}

function isLegacyHeaderValueSafe(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code === 0x09) continue;
    if (code < 0x20 || code > 0x7e) return false;
  }
  return true;
}

function utf8Base64(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

export function setActorHeaders(headers: Headers, actor: string) {
  const subject = actor.trim();
  if (!subject) return;
  if (isLegacyHeaderValueSafe(subject)) {
    headers.set("x-windforce-actor", subject);
    return;
  }
  headers.set("x-windforce-actor-utf8", utf8Base64(subject));
}

type RequestOptions = {
  method?: string;
  body?: unknown;
};

export class WindforceApi {
  constructor(private readonly settings: Settings) {}

  clients(): Promise<Client[]> {
    return this.request("/clients");
  }

  client(id: string): Promise<Client> {
    return this.request(`/clients/${encodeURIComponent(id)}`);
  }

  createClient(payload: ClientPayload): Promise<ClientTokenResult> {
    return this.request("/clients", { method: "POST", body: payload });
  }

  updateClient(id: string, payload: ClientPayload): Promise<Client> {
    return this.request(`/clients/${encodeURIComponent(id)}`, { method: "PATCH", body: payload });
  }

  async deleteClient(id: string): Promise<void> {
    await this.request(`/clients/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  rotateClientToken(id: string): Promise<ClientTokenResult> {
    return this.request(`/clients/${encodeURIComponent(id)}/token`, { method: "POST" });
  }

  revokeClientToken(id: string): Promise<Client> {
    return this.request(`/clients/${encodeURIComponent(id)}/token`, { method: "DELETE" });
  }

  clientInputConfigs(id: string): Promise<InputConfig[]> {
    return this.request(`/clients/${encodeURIComponent(id)}/input-configs`);
  }

  clientInputConfigAudit(id: string): Promise<InputConfigAudit[]> {
    return this.request(`/clients/${encodeURIComponent(id)}/input-config-audit`);
  }

  appInputConfigs(appKey: string): Promise<InputConfig[]> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/input-configs`);
  }

  appInputConfigAudit(appKey: string): Promise<InputConfigAudit[]> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/input-config-audit`);
  }

  setInputConfig(appKey: string, payload: InputConfigPayload): Promise<InputConfig> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/input-configs`, {
      method: "PUT",
      body: payload,
    });
  }

  async deleteInputConfig(appKey: string, actionKey: string, clientID = ""): Promise<void> {
    const params = new URLSearchParams();
    if (actionKey) params.set("action_key", actionKey);
    if (clientID) params.set("client_id", clientID);
    const query = params.toString();
    await this.request(
      `/apps/${encodeURIComponent(appKey)}/input-configs${query ? `?${query}` : ""}`,
      {
        method: "DELETE",
      },
    );
  }

  gitSources(): Promise<GitSource[]> {
    return this.request("/git_sources");
  }

  registerGitSource(payload: RegisterSourcePayload): Promise<GitSource> {
    return this.request("/git_sources", { method: "POST", body: payload });
  }

  probeGitSource(payload: Record<string, unknown>): Promise<ProbeResult> {
    return this.request("/git_sources/probe", { method: "POST", body: payload });
  }

  setVariable(payload: SetVariablePayload): Promise<{ path: string; is_secret: boolean }> {
    return this.request("/variables", { method: "POST", body: payload });
  }

  createSample(appKey: string): Promise<{ source: GitSource; sync_result: DeployResult }> {
    return this.request("/git_sources/sample", { method: "POST", body: { app_key: appKey } });
  }

  patchGitSource(id: number, payload: PatchSourcePayload): Promise<GitSource> {
    return this.request(`/git_sources/${id}`, { method: "PATCH", body: payload });
  }

  async deleteGitSource(id: number): Promise<void> {
    await this.request(`/git_sources/${id}`, { method: "DELETE" });
  }

  syncGitSource(id: number): Promise<SourceSyncResult> {
    return this.request(`/git_sources/${id}/sync`, { method: "POST" });
  }

  deployGitSource(id: number, message: string): Promise<DeployResult> {
    const body: Record<string, unknown> = { confirm: true };
    if (message) body.message = message;
    return this.request(`/git_sources/${id}/deploy`, { method: "POST", body });
  }

  apps(): Promise<{ apps: AppSummary[] }> {
    return this.request("/apps?view=summary");
  }

  app(appKey: string): Promise<AppDetail> {
    return this.request(`/apps/${encodeURIComponent(appKey)}`);
  }

  appHistory(appKey: string): Promise<HistoryItem[]> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/history`);
  }

  rollbackAppRelease(
    appKey: string,
    releaseID: string,
    reason: string,
  ): Promise<ReleaseRollbackResult> {
    return this.request(
      `/apps/${encodeURIComponent(appKey)}/releases/${encodeURIComponent(releaseID)}/rollback`,
      { method: "POST", body: { confirm: true, reason } },
    );
  }

  actionSchemas(appKey: string, actionKey: string): Promise<ActionSchemas> {
    return this.request(
      `/apps/${encodeURIComponent(appKey)}/actions/${encodeURIComponent(actionKey)}/schema`,
    );
  }

  appDocumentation(appKey: string): Promise<AppDocumentation> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/documentation`);
  }

  jobsSummary(recentSeconds = 86400): Promise<JobsSummary> {
    return this.request(`/jobs/summary?recent_seconds=${recentSeconds}`);
  }

  auditTrail(sourceID: number): Promise<AuditRecord[]> {
    return this.request(`/git_sources/${sourceID}/audit`);
  }

  auditEvents(query: AuditEventQuery = {}): Promise<AuditEvent[]> {
    const params = new URLSearchParams();
    if (query.appKey) params.set("app_key", query.appKey);
    if (query.clientID) params.set("client_id", query.clientID);
    if (query.category) params.set("category", query.category);
    if (query.actor) params.set("actor", query.actor);
    if (query.gitSourceID) params.set("git_source_id", String(query.gitSourceID));
    if (query.since) params.set("since", query.since);
    if (query.until) params.set("until", query.until);
    if (query.limit) params.set("limit", String(query.limit));
    const suffix = params.size ? `?${params.toString()}` : "";
    return this.request(`/audit-events${suffix}`);
  }

  webhookSubscriptions(includeDeleted = false): Promise<WebhookSubscription[]> {
    const suffix = includeDeleted ? "?include_deleted=true" : "";
    return this.request(`/webhooks${suffix}`);
  }

  webhookSubscription(id: string): Promise<WebhookSubscription> {
    return this.request(`/webhooks/${encodeURIComponent(id)}`);
  }

  createWebhookSubscription(
    payload: WebhookSubscriptionCreate,
  ): Promise<WebhookSubscriptionMutation> {
    return this.request("/webhooks", { method: "POST", body: payload });
  }

  updateWebhookSubscription(
    id: string,
    payload: WebhookSubscriptionUpdate,
  ): Promise<WebhookSubscriptionMutation> {
    return this.request(`/webhooks/${encodeURIComponent(id)}`, { method: "PATCH", body: payload });
  }

  async deleteWebhookSubscription(id: string): Promise<void> {
    await this.request(`/webhooks/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  testWebhookSubscription(id: string): Promise<WebhookDeliveryDetail> {
    return this.request(`/webhooks/${encodeURIComponent(id)}/test`, { method: "POST" });
  }

  webhookDeliveries(id: string, query: WebhookDeliveryQuery = {}): Promise<WebhookDeliveryPage> {
    const params = new URLSearchParams();
    if (query.state) params.set("state", query.state);
    if (query.limit) params.set("limit", String(query.limit));
    if (query.cursor) params.set("cursor", query.cursor);
    const suffix = params.size ? `?${params.toString()}` : "";
    return this.request(`/webhooks/${encodeURIComponent(id)}/deliveries${suffix}`);
  }

  webhookDelivery(id: string): Promise<WebhookDeliveryDetail> {
    return this.request(`/webhook-deliveries/${encodeURIComponent(id)}`);
  }

  retryWebhookDelivery(id: string): Promise<WebhookDeliveryDetail> {
    return this.request(`/webhook-deliveries/${encodeURIComponent(id)}/retry`, { method: "POST" });
  }

  importProvisioning(
    text: string,
    dryRun: boolean,
    format: "yaml" | "json",
  ): Promise<ProvisioningImportResult> {
    const suffix = dryRun ? "?dry_run=true" : "";
    return this.requestRaw(`/provisioning/import${suffix}`, {
      method: "POST",
      body: text,
      contentType: format === "yaml" ? "application/yaml" : "application/json",
    });
  }

  exportProvisioning(format: "yaml" | "json", includeValues = false): Promise<string> {
    const params = new URLSearchParams();
    params.set("format", format);
    if (includeValues) params.set("include_values", "true");
    return this.requestText(`/provisioning/export?${params.toString()}`);
  }

  systemInfo(): Promise<SystemInfo> {
    return this.request("/system/info");
  }

  workspaces(): Promise<{ items: Workspace[] }> {
    return this.globalRequest("/api/workspaces");
  }

  workspace(id: string): Promise<Workspace> {
    return this.globalRequest(`/api/workspaces/${encodeURIComponent(id)}`);
  }

  createWorkspace(id: string, name: string): Promise<WorkspaceTokenResult> {
    return this.globalRequest("/api/workspaces", { method: "POST", body: { id, name } });
  }

  updateWorkspace(id: string, name: string): Promise<Workspace> {
    return this.globalRequest(`/api/workspaces/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: { name },
    });
  }

  archiveWorkspace(id: string): Promise<Workspace> {
    return this.globalRequest(`/api/workspaces/${encodeURIComponent(id)}/archive`, {
      method: "POST",
    });
  }

  rotateWorkspaceToken(id: string): Promise<WorkspaceTokenResult> {
    return this.globalRequest(`/api/workspaces/${encodeURIComponent(id)}/token`, {
      method: "POST",
    });
  }

  workspaceAudit(id: string): Promise<{ items: WorkspaceAudit[] }> {
    return this.globalRequest(`/api/workspaces/${encodeURIComponent(id)}/audit`);
  }

  private async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.requestURL(this.workspaceURL(path), options);
  }

  private async globalRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
    return this.requestURL(path, options);
  }

  private async requestURL<T>(url: string, options: RequestOptions = {}): Promise<T> {
    const headers = new Headers();
    headers.set("accept", "application/json");
    if (this.settings.token) headers.set("authorization", `Bearer ${this.settings.token}`);
    setActorHeaders(headers, this.settings.actor);
    let body: BodyInit | undefined;
    if (options.body !== undefined) {
      headers.set("content-type", "application/json");
      body = JSON.stringify(options.body);
    }
    const response = await fetch(url, {
      method: options.method || "GET",
      headers,
      body,
    });
    const text = await response.text();
    if (!response.ok) {
      let message = `${response.status} ${response.statusText}`;
      try {
        const payload = JSON.parse(text) as { error?: string };
        if (payload?.error) message = payload.error;
      } catch {
        if (text) message = text;
      }
      throw new ApiError(message, response.status);
    }
    if (!text) return undefined as T;
    try {
      return JSON.parse(text) as T;
    } catch {
      return text as T;
    }
  }

  private async requestRaw<T>(
    path: string,
    options: { method: string; body: string; contentType: string },
  ): Promise<T> {
    const headers = new Headers();
    headers.set("accept", "application/json");
    headers.set("content-type", options.contentType);
    if (this.settings.token) headers.set("authorization", `Bearer ${this.settings.token}`);
    setActorHeaders(headers, this.settings.actor);
    const response = await fetch(this.workspaceURL(path), {
      method: options.method,
      headers,
      body: options.body,
    });
    const text = await response.text();
    if (!response.ok) throw new ApiError(apiErrorMessage(response, text), response.status);
    if (!text) return undefined as T;
    return JSON.parse(text) as T;
  }

  private async requestText(path: string): Promise<string> {
    const headers = new Headers();
    headers.set("accept", "application/yaml, application/json");
    if (this.settings.token) headers.set("authorization", `Bearer ${this.settings.token}`);
    setActorHeaders(headers, this.settings.actor);
    const response = await fetch(this.workspaceURL(path), { headers });
    const text = await response.text();
    if (!response.ok) throw new ApiError(apiErrorMessage(response, text), response.status);
    return text;
  }

  private workspaceURL(path: string): string {
    const workspace = encodeURIComponent(this.settings.workspace || "default");
    return `/api/w/${workspace}${path}`;
  }
}

function apiErrorMessage(response: Response, text: string): string {
  let message = `${response.status} ${response.statusText}`;
  try {
    const payload = JSON.parse(text) as { error?: string };
    if (payload?.error) message = payload.error;
  } catch {
    if (text) message = text;
  }
  return message;
}
