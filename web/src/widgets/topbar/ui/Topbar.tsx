"use client";

import type { ApiSettings } from "@/shared/api/types";

type Props = {
  settings: ApiSettings;
  onChange: (settings: ApiSettings) => void;
  onRefresh: () => void;
  busy: boolean;
};

export function Topbar({ settings, onChange, onRefresh, busy }: Props) {
  return (
    <header className="topbar">
      <div className="brand">
        <div className="brandMark">WF</div>
        <div>
          <h1>windforce-lite</h1>
          <p>Deployment control plane</p>
        </div>
      </div>
      <div className="settingsGrid">
        <label className="field">
          Workspace
          <input
            id="workspaceInput"
            value={settings.workspace}
            onChange={(event) => onChange({ ...settings, workspace: event.target.value.trim() || "default" })}
            spellCheck={false}
          />
        </label>
        <label className="field">
          API token
          <input
            id="tokenInput"
            type="password"
            placeholder="optional"
            value={settings.token}
            onChange={(event) => onChange({ ...settings, token: event.target.value })}
          />
        </label>
        <label className="field">
          Actor
          <input
            id="actorInput"
            placeholder="required for deploy"
            value={settings.actor}
            onChange={(event) => onChange({ ...settings, actor: event.target.value })}
            spellCheck={false}
          />
        </label>
        <button className="button" type="button" onClick={onRefresh} disabled={busy}>
          Refresh
        </button>
      </div>
    </header>
  );
}
