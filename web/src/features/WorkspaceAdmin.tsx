import { Check, Copy } from "lucide-react";
import type { Workspace } from "../lib/api";
import { useApp } from "../lib/app-context";
import { useRouter } from "../lib/router";

export function WorkspaceStatus({ workspace }: { workspace: Workspace }) {
  return workspace.status === "active" ? (
    <span className="badge badge-good">Active</span>
  ) : (
    <span className="badge badge-neutral">Archived</span>
  );
}

export function WorkspaceActivation({ workspace, compact = false }: { workspace: Workspace; compact?: boolean }) {
  const { settings, updateSettings } = useApp();
  const { navigate } = useRouter();
  const current = workspace.id === settings.workspace;

  if (current) {
    return (
      <span className="badge badge-current">
        <Check size={13} aria-hidden="true" /> Current
      </span>
    );
  }

  return (
    <button
      className={compact ? "button small primary" : "button primary"}
      type="button"
      disabled={workspace.status === "archived"}
      title={workspace.status === "archived" ? "Archived workspaces cannot be selected" : `Switch to ${workspace.name}`}
      onClick={() => {
        updateSettings({ ...settings, workspace: workspace.id });
        navigate("/");
      }}
    >
      {compact ? "Switch" : "Switch to workspace"}
    </button>
  );
}

export function OneTimeWorkspaceToken({ token }: { token: string }) {
  const { notify } = useApp();
  return (
    <div className="oneTimeToken">
      <p className="fieldLabel">One-time workspace token</p>
      <div className="copyField">
        <code>{token}</code>
        <button
          className="button iconButton"
          type="button"
          title="Copy token"
          aria-label="Copy workspace token"
          onClick={async () => {
            await navigator.clipboard.writeText(token);
            notify("ok", "Workspace token copied.");
          }}
        >
          <Copy size={16} aria-hidden="true" />
        </button>
      </div>
      <p className="fieldHint">This value is shown once. Rotating it immediately invalidates the previous token.</p>
    </div>
  );
}
