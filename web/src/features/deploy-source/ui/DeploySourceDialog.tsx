"use client";

import type { GitSource } from "@/entities/git-source";
import { formatDate, shortID } from "@/shared/lib/format";

type Props = {
  source: GitSource | null;
  actor: string;
  busy: boolean;
  error: string;
  onClose: () => void;
  onDeploy: (message: string) => Promise<void>;
  onOpenSettings?: () => void;
};

export function DeploySourceDialog({ source, actor, busy, error, onClose, onDeploy, onOpenSettings }: Props) {
  if (!source) return null;

  async function submit(form: HTMLFormElement) {
    const formData = new FormData(form);
    const confirmName = String(formData.get("confirmName") || "").trim();
    const message = String(formData.get("message") || "").trim();
    if (!source || confirmName !== source.name || !actor.trim()) return;
    await onDeploy(message);
  }

  return (
    <div id="deployDialog" className="modalBackdrop" role="presentation">
      <form
        className="modal"
        aria-label="Confirm deployment"
        onSubmit={(event) => {
          event.preventDefault();
          void submit(event.currentTarget);
        }}
      >
        <header className="dialogHeader">
          <div>
            <h2>Confirm Deployment</h2>
            <p>{source.repo_url}</p>
          </div>
          <button className="button" type="button" onClick={onClose}>
            Cancel
          </button>
        </header>
        <div className="detailGrid two">
          <Field label="Source" value={source.name} />
          <Field label="Branch" value={source.branch || "main"} />
          <Field label="Subpath" value={source.subpath || "root"} />
          <Field label="Last deployed" value={`${formatDate(source.last_synced_at)} / ${shortID(source.last_synced_commit, 14)}`} />
        </div>
        <label className="field">
          Type source name
          <input id="deployConfirmInput" name="confirmName" placeholder={source.name} autoComplete="off" />
        </label>
        <label className="field">
          Audit note
          <textarea id="deployMessage" name="message" placeholder="optional" />
        </label>
        <p className={actor.trim() ? "hint" : "hint warn"}>
          {actor.trim() ? `Actor: ${actor}` : "Set Actor in Settings before deployment."}
        </p>
        {error ? <p className="hint dangerText">{error}</p> : null}
        <div className="actions end">
          {!actor.trim() && onOpenSettings ? (
            <button
              className="button"
              type="button"
              onClick={() => {
                onClose();
                onOpenSettings();
              }}
            >
              Set Actor
            </button>
          ) : null}
          <button className="button primary" type="submit" disabled={busy || !actor.trim()}>
            {busy ? "Deploying..." : "Deploy"}
          </button>
        </div>
      </form>
    </div>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div className="kv">
      <span>{label}</span>
      <strong>{value || "-"}</strong>
    </div>
  );
}
