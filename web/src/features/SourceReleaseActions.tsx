import { Check, RefreshCw, Rocket } from "lucide-react";
import { useState } from "react";
import { errorMessage, type GitSource, type SourceSyncResult } from "../lib/api";
import { useApp } from "../lib/app-context";
import { shortSHA } from "../lib/format";
import { releaseActionState } from "../lib/release-actions";

export function SourceReleaseActions({
  source,
  activeCommit,
  compact = false,
  syncButtonID,
  publishButtonID,
  onSynced,
  onPublish,
}: {
  source: GitSource;
  activeCommit?: string;
  compact?: boolean;
  syncButtonID?: string;
  publishButtonID?: string;
  onSynced: (result: SourceSyncResult) => void;
  onPublish: (source: GitSource) => void;
}) {
  const { api, notify } = useApp();
  const [syncing, setSyncing] = useState(false);
  const [syncResult, setSyncResult] = useState<SourceSyncResult | null>(null);
  const latestCommit = syncResult?.commit || source.last_synced_commit || "";
  const state = releaseActionState(activeCommit, latestCommit, Boolean(syncResult));
  const buttonClass = compact ? "button small" : "button";

  async function syncSource() {
    setSyncing(true);
    try {
      const result = await api.syncGitSource(source.id);
      setSyncResult(result);
      if (result.commit === source.last_synced_commit) {
        notify("ok", `${result.app} is already synchronized at ${shortSHA(result.commit, 12)}.`);
      } else {
        notify("ok", `Synchronized ${result.app} at ${shortSHA(result.commit, 12)}.`);
      }
      onSynced(result);
    } catch (cause) {
      notify("error", errorMessage(cause));
    } finally {
      setSyncing(false);
    }
  }

  const effectiveSource: GitSource = syncResult
    ? { ...source, last_synced_commit: syncResult.commit, last_synced_at: syncResult.synced_at }
    : source;

  return (
    <div className="releaseActionGroup">
      <button
        className={buttonClass}
        id={syncButtonID}
        data-checked={syncResult ? "true" : "false"}
        type="button"
        disabled={syncing || state.syncDisabled}
        title={state.syncDisabled ? "The tracked branch was checked in this view." : "Check and synchronize the tracked branch."}
        onClick={syncSource}
      >
        {syncing ? <RefreshCw aria-hidden="true" className="spin" /> : state.syncDisabled ? <Check aria-hidden="true" /> : <RefreshCw aria-hidden="true" />}
        {syncing ? "Synchronizing…" : state.syncLabel}
      </button>
      <button
        className={`${buttonClass} primary`}
        id={publishButtonID}
        type="button"
        disabled={syncing || state.publishDisabled}
        title={publishButtonTitle(state.publishLabel)}
        onClick={() => onPublish(effectiveSource)}
      >
        <Rocket aria-hidden="true" />
        {state.publishLabel}
      </button>
    </div>
  );
}

function publishButtonTitle(label: "Sync required" | "Up to date" | "Publish Release"): string {
  if (label === "Sync required") return "Synchronize the source before publishing.";
  if (label === "Up to date") return "The active release already uses the latest synchronized source.";
  return "Prepare and publish the latest synchronized source.";
}
