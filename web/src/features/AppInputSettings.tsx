import { Plus } from "lucide-react";
import { useMemo, useState } from "react";
import { EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import { type AppDetail, type InputConfig } from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp, useAsync } from "../lib/app-context";
import { Link } from "../lib/router";
import { InputConfigDialog } from "./InputConfigDialog";
import { InputSettingScopeList } from "./InputSettingScopeList";

export function AppInputSettings({ detail, sourceID }: { detail: AppDetail; sourceID: number }) {
  const { api } = useApp();
  const [editing, setEditing] = useState<InputConfig | "new" | null>(null);
  const state = useAsync(
    async () => {
      const [configs, clients] = await Promise.all([
        api.appInputConfigs(detail.app.app_key),
        api.clients(),
      ]);
      return { configs, clients };
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
        subtitle="Values applied before execution. Locked values cannot be overridden by the incoming request."
        actions={
          <>
            <Link className="button" to={`/apps/${sourceID}/audit`}>View audit</Link>
            <button className="button primary" type="button" onClick={() => setEditing("new")}>
              <Plus size={16} aria-hidden="true" />
              Add settings
            </button>
          </>
        }
      >
        {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
        {state.loading && !state.data ? <Loading /> : null}
        {state.data && state.data.configs.length === 0 ? (
          <EmptyState title="No input settings for this app." />
        ) : null}
        {state.data && state.data.configs.length > 0 ? (
          <InputSettingScopeList
            id="appInputSettings"
            items={state.data.configs.map((config) => {
              const client = config.client_id ? clientsByID.get(config.client_id) : undefined;
              const action = config.action_key ? actionsByKey.get(config.action_key) : undefined;
              const actionName = action ? actionDisplayName(action.display_name) || action.action_key : "All actions";
              return {
                key: `${config.client_id || "default"}-${config.action_key || "all"}`,
                config,
                primaryLabel: "Client scope",
                primaryValue: client ? <Link to={`/clients/${client.id}`}>{client.name}</Link> : "All clients",
                primaryMeta: client ? "Client override" : "App default",
                actionName,
                actionMeta: config.action_key || "App default",
                editLabel: `Edit input settings for ${client?.name || "all clients"}, ${actionName}`,
                onEdit: () => setEditing(config),
              };
            })}
          />
        ) : null}
      </Panel>

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
