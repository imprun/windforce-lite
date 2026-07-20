import { Plus, RefreshCw } from "lucide-react";
import { useState } from "react";
import { Layout } from "../components/Layout";
import { EmptyState, ErrorNotice, Field, Loading, Modal, Panel } from "../components/ui";
import { OneTimeWorkspaceToken, WorkspaceStatus } from "../features/WorkspaceAdmin";
import { useApp, useAsync } from "../lib/app-context";
import { errorMessage } from "../lib/api";
import { formatRelative, formatTime } from "../lib/format";
import { Link } from "../lib/router";
import { notifyWorkspaceRegistryChanged } from "../lib/workspaces";

export function WorkspacesPage() {
  const { api, settings } = useApp();
  const state = useAsync(() => api.workspaces(), [api]);
  const [creating, setCreating] = useState(false);

  return (
    <Layout
      scope="instance"
      title="Workspaces"
      subtitle="Instance administration for workspace identity, access, and lifecycle."
      actions={
        <>
          <button className="button" type="button" onClick={state.reload} title="Refresh workspaces">
            <RefreshCw size={16} aria-hidden="true" /> Refresh
          </button>
          <button className="button primary" type="button" onClick={() => setCreating(true)}>
            <Plus size={16} aria-hidden="true" /> Create workspace
          </button>
        </>
      }
    >
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading label="Loading workspaces…" /> : null}
      {state.data ? (
        <Panel
          title="Workspace registry"
          subtitle={`${state.data.items.length} managed workspace${state.data.items.length === 1 ? "" : "s"} in this instance.`}
        >
          {state.data.items.length === 0 ? (
            <EmptyState title="No workspaces registered." />
          ) : (
            <div className="tableWrap">
              <table className="table workspaceTable" id="workspaceRegistry">
                <thead>
                  <tr>
                    <th>Workspace</th>
                    <th>Status</th>
                    <th>Access token</th>
                    <th>Updated</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {state.data.items.map((workspace) => (
                    <tr key={workspace.id}>
                      <td>
                        <Link className="cellTitle tablePrimaryLink" to={`/workspaces/${encodeURIComponent(workspace.id)}`}>
                          {workspace.name}
                        </Link>
                        <span className="cellSub mono">
                          {workspace.id}{workspace.id === settings.workspace ? " · current" : ""}
                        </span>
                      </td>
                      <td><WorkspaceStatus workspace={workspace} /></td>
                      <td>{workspace.has_token ? "Configured" : "Not configured"}</td>
                      <td title={formatTime(workspace.updated_at)}>
                        <span className="cellTitle">{formatRelative(workspace.updated_at)}</span>
                        <span className="cellSub">{workspace.updated_by}</span>
                      </td>
                      <td className="tableActions">
                        <Link className="button small" to={`/workspaces/${encodeURIComponent(workspace.id)}`}>Manage</Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>
      ) : null}

      {creating ? (
        <CreateWorkspaceDialog
          onClose={() => setCreating(false)}
          onCreated={() => {
            state.reload();
            notifyWorkspaceRegistryChanged();
          }}
        />
      ) : null}
    </Layout>
  );
}

function CreateWorkspaceDialog({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const { api, notify } = useApp();
  const [id, setID] = useState("");
  const [name, setName] = useState("");
  const [token, setToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  async function create() {
    setSaving(true);
    setError("");
    try {
      const result = await api.createWorkspace(id.trim(), name.trim());
      setToken(result.api_token);
      onCreated();
      notify("ok", `Workspace ${result.workspace.id} created.`);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      title={token ? "Workspace created" : "Create workspace"}
      subtitle={token ? "Store the workspace token now; it will not be shown again." : "Workspace IDs are permanent routing identifiers."}
      onClose={onClose}
    >
      {error ? <ErrorNotice message={error} /> : null}
      {token ? (
        <OneTimeWorkspaceToken token={token} />
      ) : (
        <div className="dialogForm">
          <Field label="Workspace ID" hint="Lowercase letters, digits, and hyphens. Cannot be changed later.">
            <input value={id} onChange={(event) => setID(event.target.value)} placeholder="team-a" autoFocus />
          </Field>
          <Field label="Display name">
            <input value={name} onChange={(event) => setName(event.target.value)} placeholder="Team A" />
          </Field>
          <div className="dialogFooter">
            <button className="button primary" type="button" disabled={saving || !id.trim() || !name.trim()} onClick={create}>
              {saving ? "Creating…" : "Create workspace"}
            </button>
          </div>
        </div>
      )}
    </Modal>
  );
}
