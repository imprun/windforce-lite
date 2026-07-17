export type ReleaseActionState = {
  syncLabel: "Sync source" | "Source current";
  syncDisabled: boolean;
  publishLabel: "Sync required" | "Up to date" | "Publish Release";
  publishDisabled: boolean;
};

export function releaseActionState(
  activeCommit: string | null | undefined,
  latestSyncedCommit: string | null | undefined,
  sourceChecked: boolean,
): ReleaseActionState {
  const active = activeCommit?.trim() || "";
  const latest = latestSyncedCommit?.trim() || "";

  return {
    syncLabel: sourceChecked ? "Source current" : "Sync source",
    syncDisabled: sourceChecked,
    publishLabel: !latest ? "Sync required" : active === latest ? "Up to date" : "Publish Release",
    publishDisabled: !latest || active === latest,
  };
}
