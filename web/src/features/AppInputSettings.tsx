import { Lock, Pencil, Plus } from "lucide-react";
import { useMemo, useState } from "react";
import { EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import { type AppDetail, type InputConfig } from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, formatTime } from "../lib/format";
import { Link } from "../lib/router";
import { InputConfigDialog } from "./InputConfigDialog";

export function AppInputSettings({ detail }: { detail: AppDetail }) {
  const { api } = useApp();
  const [editing, setEditing] = useState<InputConfig | "new" | null>(null);
  const state = useAsync(
    async () => {
      const [configs, clients, audit] = await Promise.all([
        api.appInputConfigs(detail.app.app_key),
        api.clients(),
        api.appInputConfigAudit(detail.app.app_key),
      ]);
      return { configs, clients, audit };
    },
    [api, detail.app.app_key],
  );
  const clientsByID = useMemo(
    () => new Map((state.data?.clients || []).map((client) => [client.id, client])),
    [state.data?.clients],
  );
  const actionsByKey = useMemo(
    () => new Map(detail.actions.map((action) => [action.action_key, action])),
    [detail.actions],
  );

  function finish() {
    setEditing(null);
    state.reload();
  }

  return (
    <>
      <Panel
        title="Input settings"
        subtitle="Default and client-specific values applied to this app before execution."
        actions={
          <button className="button primary" type="button" onClick={() => setEditing("new")}>
            <Plus size={16} aria-hidden="true" />
            Add settings
          </button>
        }
      >
        {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
        {state.loading && !state.data ? <Loading /> : null}
        {state.data && state.data.configs.length === 0 ? (
          <EmptyState title="No input settings for this app." />
        ) : null}
        {state.data && state.data.configs.length > 0 ? (
          <div className="tableWrap">
            <table className="table inputSettingsTable" id="appInputSettings">
              <thead>
                <tr>
                  <th>Client scope</th>
                  <th>Action scope</th>
                  <th>Keys</th>
                  <th>Locked</th>
                  <th>Updated</th>
                  <th>Actor</th>
                  <th aria-label="Row actions" />
                </tr>
              </thead>
              <tbody>
                {state.data.configs.map((config) => {
                  const client = config.client_id ? clientsByID.get(config.client_id) : undefined;
                  const action = config.action_key ? actionsByKey.get(config.action_key) : undefined;
                  return (
                    <tr key={`${config.client_id || "default"}-${config.action_key || "all"}`}>
                      <td>
                        {client ? (
                          <Link className="cellTitle" to={`/clients/${client.id}`}>
                            {client.name}
                          </Link>
                        ) : (
                          <span className="cellTitle">All clients</span>
                        )}
                        <span className="cellSub">{client ? "client override" : "default"}</span>
                      </td>
                      <td>
                        <span className="cellTitle">{action ? actionDisplayName(action.display_name) || action.action_key : "All actions"}</span>
                        <span className="cellSub mono">{config.action_key || "app default"}</span>
                      </td>
                      <td>{Object.keys(config.config).length}</td>
                      <td>
                        {config.locked_keys.length ? (
                          <span className="lockedCount">
                            <Lock size={14} aria-hidden="true" /> {config.locked_keys.length}
                          </span>
                        ) : (
                          "0"
                        )}
                      </td>
                      <td title={formatTime(config.updated_at)}>
                        <span className="cellTitle">{formatRelative(config.updated_at)}</span>
                        <span className="cellSub">{formatTime(config.updated_at)}</span>
                      </td>
                      <td>{config.updated_by}</td>
                      <td className="rowActions">
                        <button
                          className="button small iconButton"
                          type="button"
                          title="Edit input settings"
                          aria-label="Edit input settings"
                          onClick={() => setEditing(config)}
                        >
                          <Pencil size={15} aria-hidden="true" />
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </Panel>

      {state.data?.audit.length ? (
        <Panel title="Input settings audit" subtitle="Configuration changes without stored input values.">
          <div className="tableWrap">
            <table className="table" id="appInputSettingsAudit">
              <thead>
                <tr>
                  <th>When</th>
                  <th>Actor</th>
                  <th>Scope</th>
                  <th>Change</th>
                  <th>Summary</th>
                </tr>
              </thead>
              <tbody>
                {state.data.audit.slice(0, 20).map((record) => (
                  <tr key={record.id}>
                    <td title={formatTime(record.created_at)}>{formatRelative(record.created_at)}</td>
                    <td>{record.actor}</td>
                    <td className="mono">
                      {record.client_id ? clientsByID.get(record.client_id)?.name || record.client_id : "all clients"} / {record.action_key || "all actions"}
                    </td>
                    <td>{record.kind}</td>
                    <td className="mono">{record.detail || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Panel>
      ) : null}

      {editing && state.data ? (
        <InputConfigDialog
          appKey={detail.app.app_key}
          actions={detail.actions}
          clients={state.data.clients}
          existing={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={finish}
        />
      ) : null}
    </>
  );
}
