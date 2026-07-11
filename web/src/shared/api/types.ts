export type ApiSettings = {
  workspace: string;
  token: string;
  actor: string;
};

export type ApiErrorPayload = {
  error?: string;
};

export type VariableRow = {
  path: string;
  app_key?: string;
};

export type WorkerTagsResponse = {
  tags?: Array<{
    tag: string;
    live_workers: number;
    capabilities?: string[];
  }>;
};
