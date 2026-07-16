import { Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { DefinitionList, EmptyState, ErrorNotice, Loading, Modal, Panel } from "../components/ui";
import { ClientDialog } from "../features/ClientDialog";
import { InputConfigDialog } from "../features/InputConfigDialog";
import { InputSettingScopeList } from "../features/InputSettingScopeList";
import { AuditEventTable } from "../features/AuditEventTable";
import { type InputConfig } from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp, useAsync } from "../lib/app-context";
import { formatTime } from "../lib/format";
import { Link, useRouter } from "../lib/router";

type EditingConfig = { appKey: string; config?: InputConfig };

export function ClientDetailPage({ clientID }: { clientID: string }) {
  const { api } = useApp();
  const { navigate } = useRouter();
  const [editingClient, setEditingClient] = useState(false);
  const [editingConfig, setEditingConfig] = useState<EditingConfig | null>(null);
  const [selectedApp, setSelectedApp] = useState("");
  const state = useAsync(
    async () => {
      const [client, configs, auditEvents, apps] = await Promise.all([
        api.client(clientID),
        api.clientInputConfigs(clientID),
        api.auditEvents({ clientID, limit: 250 }),
        api.apps(),
      ]);
      const releasedApps = apps.apps || [];
      const appDetailResults = await Promise.allSettled(releasedApps.map((app) => api.app(app.app_key)));
      const appDetails = appDetailResults.flatMap((result) => result.status === "fulfilled" ? [result.value] : []);
      return { client, configs, auditEvents, apps: releasedApps, appDetails };
    },
    [api, clientID],
  );

  useEffect(() => {
    if (!selectedApp && state.data?.apps.length) setSelectedApp(state.data.apps[0].app_key);
  }, [selectedApp, state.data?.apps]);

  const appsByKey = useMemo(
    () => new Map((state.data?.apps || []).map((app) => [app.app_key, app])),
    [state.data?.apps],
  );
  const appDetailsByKey = useMemo(
    () => new Map((state.data?.appDetails || []).map((detail) => [detail.app.app_key, detail])),
    [state.data?.appDetails],
  );

  if (state.loading && !state.data) {
    return (
      <Layout title="Client Registry">
        <Loading />
      </Layout>
    );
  }
  if (state.error || !state.data) {
    return (
      <Layout title="Client not found">
        <ErrorNotice message={state.error || "Client not found."} onRetry={state.reload} />
      </Layout>
    );
  }

  const { client, configs, auditEvents, apps } = state.data;
  function finishConfig() {
    setEditingConfig(null);
    state.reload();
  }

  return (
    <Layout
      title={client.name}
      subtitle="External client input settings across released apps."
      actions={
        <>
          <Link className="button" to="/clients">
            Back to registry
          </Link>
          <button className="button" type="button" onClick={() => setEditingClient(true)}>
            Edit client
          </button>
        </>
      }
    >
      <Panel title="Client identity" subtitle="Used by trusted trigger adapters to select client-specific settings.">
        <DefinitionList
          items={[
            ["Name", client.name],
            ["External key", <span className="mono">{client.external_key}</span>],
            ["Updated", formatTime(client.updated_at)],
            ["Updated by", client.updated_by],
          ]}
        />
      </Panel>

      <Panel
        title="Input settings"
        subtitle="App- and action-scoped values assigned to this external client."
        actions={
          apps.length ? (
            <div className="inlineActions">
              <select value={selectedApp} aria-label="App for new input settings" onChange={(event) => setSelectedApp(event.target.value)}>
                {apps.map((app) => (
                  <option key={app.app_key} value={app.app_key}>
                    {app.app_key}
                  </option>
                ))}
              </select>
              <button className="button primary" type="button" disabled={!selectedApp} onClick={() => setEditingConfig({ appKey: selectedApp })}>
                <Plus size={16} aria-hidden="true" />
                Add settings
              </button>
            </div>
          ) : null
        }
      >
        {configs.length === 0 ? (
          <EmptyState title={apps.length ? "No input settings for this client." : "No released apps are available."} />
        ) : (
          <InputSettingScopeList
            id="clientInputSettings"
            items={configs.map((config) => {
              const app = appsByKey.get(config.app_key);
              const action = appDetailsByKey.get(config.app_key)?.actions.find((item) => item.action_key === config.action_key);
              const actionName = action ? actionDisplayName(action.display_name) || action.action_key : config.action_key || "All actions";
              return {
                key: `${config.app_key}-${config.action_key || "all"}`,
                config,
                primaryLabel: "App",
                primaryValue: app ? <Link to={`/apps/${app.git_source_id}/input-settings`}>{config.app_key}</Link> : config.app_key,
                primaryMeta: app ? "Released app" : "Release unavailable",
                actionName,
                actionMeta: config.action_key ? `${config.action_key} · Action override` : "App-wide client override",
                editLabel: `Edit ${config.app_key} input settings for ${actionName}`,
                editDisabled: !app,
                onEdit: () => setEditingConfig({ appKey: config.app_key, config }),
              };
            })}
          />
        )}
      </Panel>

      <Panel title="Audit trail" subtitle="Registry and input-setting changes for this client.">
        <AuditEventTable events={auditEvents} emptyTitle="No changes have been recorded for this client." />
      </Panel>

      {editingClient ? (
        <ClientDialog
          client={client}
          onClose={() => setEditingClient(false)}
          onSaved={() => {
            setEditingClient(false);
            state.reload();
          }}
          onDeleted={() => navigate("/clients")}
        />
      ) : null}
      {editingConfig ? (
        <ClientInputConfigDialog
          clientID={client.id}
          appKey={editingConfig.appKey}
          existing={editingConfig.config}
          onClose={() => setEditingConfig(null)}
          onSaved={finishConfig}
        />
      ) : null}
    </Layout>
  );
}

function ClientInputConfigDialog({
  clientID,
  appKey,
  existing,
  onClose,
  onSaved,
}: {
  clientID: string;
  appKey: string;
  existing?: InputConfig;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { api } = useApp();
  const state = useAsync(async () => Promise.all([api.app(appKey), api.clients()]), [api, appKey]);
  if (state.error) {
    return (
      <Modal title="Input Settings" onClose={onClose}>
        <ErrorNotice message={state.error} onRetry={state.reload} />
      </Modal>
    );
  }
  if (!state.data) {
    return (
      <Modal title="Input Settings" onClose={onClose}>
        <Loading />
      </Modal>
    );
  }
  const [detail, clients] = state.data;
  return (
    <InputConfigDialog
      appKey={appKey}
      actions={detail.actions}
      clients={clients}
      existing={existing}
      fixedClientID={clientID}
      onClose={onClose}
      onSaved={onSaved}
    />
  );
}
