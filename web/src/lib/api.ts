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
  external_key: string;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
};

export type ClientPayload = {
  name: string;
  external_key: string;
};

export type ProbeResult = {
  reachable: boolean;
  branch?: string;
  branch_exists?: boolean;
  branches?: string[];
  error?: string;
};

export type SyncResult = {
  commit: string;
  app: string;
  actions: string[];
  source?: string;
  deployment_id?: string;
  created_by?: string;
  message?: string;
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
  deployment_id?: string;
  message?: string;
  created_by?: string;
  created_at: string;
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

  createClient(payload: ClientPayload): Promise<Client> {
    return this.request("/clients", { method: "POST", body: payload });
  }

  updateClient(id: string, payload: ClientPayload): Promise<Client> {
    return this.request(`/clients/${encodeURIComponent(id)}`, { method: "PATCH", body: payload });
  }

  async deleteClient(id: string): Promise<void> {
    await this.request(`/clients/${encodeURIComponent(id)}`, { method: "DELETE" });
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

  createSample(appKey: string): Promise<{ source: GitSource; sync_result: SyncResult }> {
    return this.request("/git_sources/sample", { method: "POST", body: { app_key: appKey } });
  }

  patchGitSource(id: number, payload: PatchSourcePayload): Promise<GitSource> {
    return this.request(`/git_sources/${id}`, { method: "PATCH", body: payload });
  }

  async deleteGitSource(id: number): Promise<void> {
    await this.request(`/git_sources/${id}`, { method: "DELETE" });
  }

  deployGitSource(id: number, message: string): Promise<SyncResult> {
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

  private async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const headers = new Headers();
    headers.set("accept", "application/json");
    if (this.settings.token) headers.set("authorization", `Bearer ${this.settings.token}`);
    setActorHeaders(headers, this.settings.actor);
    let body: BodyInit | undefined;
    if (options.body !== undefined) {
      headers.set("content-type", "application/json");
      body = JSON.stringify(options.body);
    }
    const response = await fetch(this.workspaceURL(path), {
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

  private workspaceURL(path: string): string {
    const workspace = encodeURIComponent(this.settings.workspace || "default");
    return `/api/w/${workspace}${path}`;
  }
}
