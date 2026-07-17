import { useState } from "react";
import { errorMessage, type GitSource, type SyncResult } from "../lib/api";
import { useApp } from "../lib/app-context";
import { shortSHA } from "../lib/format";
import { DefinitionList, Field, Modal } from "../components/ui";
import { Link } from "../lib/router";

export function PublishReleaseDialog({
  source,
  appKey,
  onClose,
  onPublished,
}: {
  source: GitSource;
  appKey?: string;
  onClose: () => void;
  onPublished: (result: SyncResult) => void;
}) {
  const { api, settings, notify } = useApp();
  const [message, setMessage] = useState("");
  const [candidate, setCandidate] = useState<SyncResult | null>(null);
  const [operation, setOperation] = useState<"" | "sync" | "publish">("");
  const [error, setError] = useState("");

  async function handleSync() {
    setOperation("sync");
    setError("");
    try {
      const result = await api.syncGitSource(source.id);
      setCandidate(result);
      notify("ok", `Synchronized ${result.app} at ${shortSHA(result.commit, 12)}.`);
    } catch (cause) {
      setCandidate(null);
      setError(errorMessage(cause));
    } finally {
      setOperation("");
    }
  }

  async function handlePublish() {
    if (!candidate) return;
    setOperation("publish");
    setError("");
    try {
      const result = await api.deployGitSource(source.id, candidate.commit, message.trim());
      notify("ok", `Published ${result.app} at ${shortSHA(result.commit, 12)}.`);
      onPublished(result);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setOperation("");
    }
  }

  const busy = operation !== "";

  return (
    <Modal
      id="publishReleaseDialog"
      title={`Publish Release — ${appKey || source.name}`}
      subtitle="Synchronize and validate a commit, then publish that exact candidate to workers."
      onClose={onClose}
    >
      <DefinitionList
        items={[
          ["Repository source", source.name],
          ["Repository", source.repo_url],
          ["Branch", source.branch || "main"],
          ["Subpath", source.subpath || "(repo root)"],
          ["Last synchronized", source.last_synced_commit ? shortSHA(source.last_synced_commit, 12) : "not synchronized yet"],
          ["Actor", settings.actor || "(not set)"],
        ]}
      />
      {candidate ? (
        <div className="inlineNotice success">
          Candidate ready: <strong>{candidate.app}</strong> at <code>{shortSHA(candidate.commit, 12)}</code> with {candidate.actions.length} action(s).
          Publishing will use this exact commit.
        </div>
      ) : (
        <div className="inlineNotice">Synchronize the repository to create a validated release candidate. No active release changes during this step.</div>
      )}
      {!settings.actor ? (
        <div className="inlineNotice error">
          Publishing requires an audit actor. Set one in <Link to="/settings">Settings</Link>.
        </div>
      ) : null}
      <Field label="Release note" hint="Recorded in release history (optional).">
        <input
          id="publishReleaseMessage"
          value={message}
          onChange={(event) => setMessage(event.target.value)}
          placeholder="What changed in this release?"
        />
      </Field>
      {error ? <div className="inlineNotice error">{error}</div> : null}
      <footer className="dialogFooter">
        <span />
        <div className="dialogFooterActions">
          <button className="button" type="button" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button className="button" type="button" onClick={handleSync} disabled={busy}>
            {operation === "sync" ? "Synchronizing…" : candidate ? "Sync again" : "Sync source"}
          </button>
          <button
            className="button primary"
            type="button"
            onClick={handlePublish}
            disabled={busy || !settings.actor || !candidate}
          >
            {operation === "publish" ? "Publishing…" : "Publish candidate"}
          </button>
        </div>
      </footer>
    </Modal>
  );
}
