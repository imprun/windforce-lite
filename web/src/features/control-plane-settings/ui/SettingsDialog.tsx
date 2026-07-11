"use client";

import { useEffect, useState } from "react";
import type { ApiSettings } from "@/shared/api/types";

type Props = {
  open: boolean;
  settings: ApiSettings;
  onClose: () => void;
  onSave: (settings: ApiSettings) => void;
};

export function SettingsDialog({ open, settings, onClose, onSave }: Props) {
  const [draft, setDraft] = useState(settings);

  useEffect(() => {
    if (open) setDraft(settings);
  }, [open, settings]);

  if (!open) return null;

  return (
    <div className="modalBackdrop" role="presentation">
      <form
        id="settingsDialog"
        className="dialog settingsDialog"
        aria-label="Control plane settings"
        onSubmit={(event) => {
          event.preventDefault();
          onSave({
            workspace: draft.workspace.trim() || "default",
            token: draft.token,
            actor: draft.actor.trim(),
          });
          onClose();
        }}
      >
        <header className="dialogHeader">
          <div>
            <span className="eyebrow">Control plane context</span>
            <h2>Settings</h2>
            <p>Requests use this workspace, API token, and deployment actor.</p>
          </div>
          <button className="button" type="button" onClick={onClose}>
            Cancel
          </button>
        </header>

        <div className="formStack">
          <label className="field">
            Workspace
            <input
              id="workspaceInput"
              value={draft.workspace}
              onChange={(event) => setDraft({ ...draft, workspace: event.target.value })}
              spellCheck={false}
            />
          </label>
          <label className="field">
            API token
            <input
              id="tokenInput"
              type="password"
              placeholder="Optional"
              value={draft.token}
              onChange={(event) => setDraft({ ...draft, token: event.target.value })}
            />
          </label>
          <label className="field">
            Actor
            <input
              id="actorInput"
              placeholder="Required for deploy"
              value={draft.actor}
              onChange={(event) => setDraft({ ...draft, actor: event.target.value })}
              spellCheck={false}
            />
          </label>
        </div>

        <div className="actions end">
          <button className="button primary" type="submit">
            Apply
          </button>
        </div>
      </form>
    </div>
  );
}
