import type { ApiErrorPayload, ApiSettings, VariableRow, WorkerTagsResponse } from "./types";
import type { AppDetail, AppHistoryItem, AppSummary, GitSource, GitSourceProbeResult, GitSourceSyncResult } from "@/shared/api/windforce";

export type RequestOptions = {
  method?: string;
  body?: unknown;
};

export class WindforceApi {
  constructor(private readonly settings: ApiSettings) {}

  async variables(): Promise<VariableRow[]> {
    return this.request("/variables");
  }

  async workerTags(): Promise<WorkerTagsResponse> {
    return this.request("/worker-tags");
  }

  async gitSources(): Promise<GitSource[]> {
    return this.request("/git_sources");
  }

  async apps(): Promise<{ apps: AppSummary[] }> {
    return this.request("/apps?view=summary");
  }

  async app(appKey: string): Promise<AppDetail> {
    return this.request(`/apps/${encodeURIComponent(appKey)}`);
  }

  async appHistory(appKey: string): Promise<AppHistoryItem[]> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/history`);
  }

  async appSource(appKey: string): Promise<{ files: Record<string, string>; skipped?: string[] }> {
    return this.request(`/apps/${encodeURIComponent(appKey)}/source`);
  }

  async registerGitSource(body: Record<string, unknown>): Promise<GitSource> {
    return this.request("/git_sources", { method: "POST", body });
  }

  async probeGitSource(body: Record<string, unknown>): Promise<GitSourceProbeResult> {
    return this.request("/git_sources/probe", { method: "POST", body });
  }

  async sample(appKey: string): Promise<unknown> {
    return this.request("/git_sources/sample", { method: "POST", body: { app_key: appKey } });
  }

  async deployGitSource(sourceID: number, body: { confirm: true; message?: string }): Promise<GitSourceSyncResult> {
    return this.request(`/git_sources/${encodeURIComponent(String(sourceID))}/deploy`, {
      method: "POST",
      body,
    });
  }

  async deleteGitSource(sourceID: number): Promise<void> {
    await this.request(`/git_sources/${encodeURIComponent(String(sourceID))}`, { method: "DELETE" });
  }

  private async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const headers = new Headers();
    headers.set("accept", "application/json");
    if (this.settings.token) headers.set("authorization", `Bearer ${this.settings.token}`);
    if (this.settings.actor) headers.set("x-windforce-actor", this.settings.actor);
    let body: BodyInit | undefined;
    if (options.body !== undefined) {
      headers.set("content-type", "application/json");
      body = JSON.stringify(options.body);
    }
    const response = await fetch(`/api/w/${encodeURIComponent(this.settings.workspace)}${path}`, {
      method: options.method || "GET",
      headers,
      body,
    });
    const text = await response.text();
    const data = parseResponse(text);
    if (!response.ok) {
      const payload = data as ApiErrorPayload;
      throw new Error(payload?.error || `${response.status} ${response.statusText}`);
    }
    return data as T;
  }
}

function parseResponse(text: string): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}
