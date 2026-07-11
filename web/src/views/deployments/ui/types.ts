import type { AppDetail, AppHistoryItem, AppSummary } from "@/entities/app";
import type { GitSource } from "@/entities/git-source";

export type Notice = {
  tone: "info" | "ok" | "error";
  text: string;
};

export type ConsoleSection = "apps" | "releases" | "audit" | "settings";

export type DetailTab = "contract" | "history" | "source";

export type DetailPage = { kind: "app"; sourceID: number };

export type DeploymentSelection = {
  source: GitSource | null;
  app: AppSummary | null;
  detail: AppDetail | null;
  history: AppHistoryItem[];
  sourceFiles: Record<string, string>;
};
