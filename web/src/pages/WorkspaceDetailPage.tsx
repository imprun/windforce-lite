import { Archive, KeyRound } from "lucide-react";
import { useEffect, useState } from "react";
import { Layout } from "../components/Layout";
import { DefinitionList, EmptyState, ErrorNotice, Field, Loading, Panel } from "../components/ui";
import { OneTimeWorkspaceToken, WorkspaceStatus } from "../features/WorkspaceAdmin";
import { useApp, useAsync } from "../lib/app-context";
import type { Workspace } from "../lib/api";
import { errorMessage } from "../lib/api";
import { formatTime } from "../lib/format";
import { Link } from "../lib/router";
import { notifyWorkspaceRegistryChanged } from "../lib/workspaces";

export const workspaceDetailTabs = [
  { key: "overview", label: "Overview" },
  { key: "access", label: "Access" },
  { key: "audit", label: "Audit" },
  { key: "lifecycle", label: "Lifecycle" },
] as const;

type WorkspaceTab = (typeof workspaceDetailTabs)[number]["key"];

export function WorkspaceDetailPage({ workspaceID, tab }: { workspaceID: string; tab: string }) {
  const { api } = useApp();
  const state = useAsync(() => api.workspace(workspaceID), [api, workspaceID]);
  const activeTab = (workspaceDetailTabs.find((item) => item.key === tab)?.key || "overview") as WorkspaceTab;

  if (state.loading && !state.data) {
    return <Layout title="Workspace"><Loading label="Loading workspace…" /></Layout>;
  }
  if (state.error || !state.data) {
    return (
      <Layout title="Workspace not found">
        <ErrorNotice message={state.error || "Workspace not found."} onRetry={state.reload} />
      </Layout>
    );
  }

  const workspace = state.data;
  return (
    <Layout
      title={workspace.name}
      subtitle={`Instance workspace · ${workspace.id}`}
      actions={<Link className="button" to="/workspaces">Back to workspaces</Link>}
    >
      <nav className="tabBar" aria-label="Workspace detail tabs">
        {workspaceDetailTabs.map((item) => (
          <Link
            key={item.key}
            className={item.key === activeTab ? "tab active" : "tab"}
            to={item.key === "overview" ? `/workspaces/${encodeURIComponent(workspace.id)}` : `/workspaces/${encodeURIComponent(workspace.id)}/${item.key}`}
          >
            {item.label}
          </Link>
        ))}
      </nav>

      {activeTab === "overview" ? <WorkspaceOverview workspace={workspace} onChanged={state.reload} /> : null}
      {activeTab === "access" ? <WorkspaceAccess workspace={workspace} onChanged={state.reload} /> : null}
      {activeTab === "audit" ? <WorkspaceAudit workspaceID={workspace.id} /> : null}
      {activeTab === "lifecycle" ? <WorkspaceLifecycle workspace={workspace} onChanged={state.reload} /> : null}
    </Layout>
  );
}

function WorkspaceOverview({ workspace, onChanged }: { workspace: Workspace; onChanged: () => void }) {
  const { api, notify } = useApp();
  const [name, setName] = useState(workspace.name);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => setName(workspace.name), [workspace.name]);

  async function save() {
    setSaving(true);
    setError("");
    try {
      await api.updateWorkspace(workspace.id, name.trim());
      notify("ok", "Workspace name updated.");
      notifyWorkspaceRegistryChanged();
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel title="Workspace identity" subtitle="The immutable routing ID and operator-facing display name.">
      {error ? <ErrorNotice message={error} /> : null}
      <DefinitionList items={[
        ["Workspace ID", <span className="mono">{workspace.id}</span>],
        ["Status", <WorkspaceStatus workspace={workspace} />],
        ["Created", formatTime(workspace.created_at)],
        ["Created by", workspace.created_by],
      ]} />
      <div className="workspaceSingleSetting">
        <Field label="Display name" hint="Shown in the workspace switcher and administration screens.">
          <input value={name} disabled={workspace.status === "archived"} onChange={(event) => setName(event.target.value)} />
        </Field>
        <button
          className="button primary"
          type="button"
          disabled={saving || workspace.status === "archived" || !name.trim() || name.trim() === workspace.name}
          onClick={save}
        >
          {saving ? "Saving…" : "Save display name"}
        </button>
      </div>
    </Panel>
  );
}

function WorkspaceAccess({ workspace, onChanged }: { workspace: Workspace; onChanged: () => void }) {
  const { api, notify } = useApp();
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function rotateToken() {
    const action = workspace.has_token ? "Rotate" : "Create";
    const consequence = workspace.has_token ? " The current token will stop working immediately." : "";
    if (!window.confirm(`${action} the access token for ${workspace.name}?${consequence}`)) return;
    setSaving(true);
    setError("");
    try {
      const result = await api.rotateWorkspaceToken(workspace.id);
      setToken(result.api_token);
      notify("ok", workspace.has_token ? "Workspace token rotated." : "Workspace token created.");
      notifyWorkspaceRegistryChanged();
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel title="Workspace access" subtitle="Scoped API access for this workspace only.">
      {error ? <ErrorNotice message={error} /> : null}
      <DefinitionList items={[
        ["Token status", workspace.has_token ? "Configured" : "Not configured"],
        ["Scope", <span className="mono">/api/w/{workspace.id}</span>],
        ["Last changed by", workspace.updated_by],
        ["Last changed", formatTime(workspace.updated_at)],
      ]} />
      <div className="workspaceCommandRow">
        <div>
          <strong>{workspace.has_token ? "Rotate access token" : "Create access token"}</strong>
          <p>{workspace.has_token ? "Creates a new one-time token and immediately invalidates the current token." : "Creates the first one-time API token for this workspace."}</p>
        </div>
        <button className="button" type="button" disabled={saving || workspace.status === "archived"} onClick={rotateToken}>
          <KeyRound size={16} aria-hidden="true" /> {saving ? "Saving…" : workspace.has_token ? "Rotate token" : "Create token"}
        </button>
      </div>
      {token ? <OneTimeWorkspaceToken token={token} /> : null}
    </Panel>
  );
}

function WorkspaceAudit({ workspaceID }: { workspaceID: string }) {
  const { api } = useApp();
  const state = useAsync(() => api.workspaceAudit(workspaceID), [api, workspaceID]);

  return (
    <Panel title="Lifecycle audit" subtitle="Workspace identity, access, and lifecycle changes.">
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading label="Loading audit…" /> : null}
      {state.data?.items.length === 0 ? <EmptyState title="No lifecycle events." /> : null}
      {state.data?.items.length ? (
        <div className="tableWrap">
          <table className="table workspaceAuditTable">
            <thead><tr><th>Event</th><th>Actor</th><th>Detail</th><th>When</th></tr></thead>
            <tbody>
              {state.data.items.map((record) => (
                <tr key={record.id}>
                  <td className="cellTitle">{record.kind.replaceAll("_", " ")}</td>
                  <td>{record.actor}</td>
                  <td>{record.detail || "—"}</td>
                  <td>{formatTime(record.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </Panel>
  );
}

function WorkspaceLifecycle({ workspace, onChanged }: { workspace: Workspace; onChanged: () => void }) {
  const { api, settings, updateSettings, notify } = useApp();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function archive() {
    if (!window.confirm(`Archive ${workspace.name}? Reads remain available, but releases, settings changes, and new runs will be blocked.`)) return;
    setSaving(true);
    setError("");
    try {
      await api.archiveWorkspace(workspace.id);
      if (workspace.id === settings.workspace) {
        updateSettings({ ...settings, workspace: "default" });
        notify("info", "Archived workspace. Switched to default.");
      } else {
        notify("ok", "Workspace archived.");
      }
      notifyWorkspaceRegistryChanged();
      onChanged();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Panel title="Workspace lifecycle" subtitle="Archive preserves records while preventing future changes and executions.">
      {error ? <ErrorNotice message={error} /> : null}
      <DefinitionList items={[["Current status", <WorkspaceStatus workspace={workspace} />], ["Workspace ID", <span className="mono">{workspace.id}</span>]]} />
      {workspace.id === "default" ? (
        <div className="inlineNotice">The default workspace is permanent and cannot be archived.</div>
      ) : workspace.status === "archived" ? (
        <div className="inlineNotice">This workspace is archived. Reads and audit records remain available.</div>
      ) : (
        <div className="dangerZone">
          <div>
            <strong>Archive workspace</strong>
            <p>Blocks releases, configuration changes, webhook changes, and new runs. This action cannot be reversed.</p>
          </div>
          <button className="button danger" type="button" disabled={saving} onClick={archive}>
            <Archive size={16} aria-hidden="true" /> {saving ? "Archiving…" : "Archive workspace"}
          </button>
        </div>
      )}
    </Panel>
  );
}
