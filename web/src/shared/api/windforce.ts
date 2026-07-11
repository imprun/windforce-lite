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

export type GitSourceProbeResult = {
  reachable: boolean;
  branch?: string;
  branch_exists?: boolean;
  branches?: string[];
  error?: string;
};

export type GitSourceSyncResult = {
  commit: string;
  app: string;
  actions: string[];
  source?: string;
  deployment_id?: string | null;
  created_by?: string | null;
  message?: string | null;
};

export type AppSummary = {
  app_key: string;
  git_source_id: number;
  commit_sha: string;
  entrypoint: string;
  tag: string;
  effective_route_tag: string;
  script_lang: string;
  updated_at: string;
  actions_count: number;
  required_capabilities?: string[];
};

export type AppDetail = {
  app: AppSummary & {
    timeout_s?: number;
    max_concurrent?: number | null;
  };
  actions: Array<{
    action_key: string;
    input_schema?: unknown;
    output_schema?: unknown;
    effective_capabilities?: string[];
    effective_route_tag?: string;
  }>;
};

export type AppHistoryItem = {
  id: string;
  commit_sha: string;
  entrypoint: string;
  source: string;
  deployment_id?: string | null;
  message?: string | null;
  created_by?: string | null;
  created_at: string;
};
