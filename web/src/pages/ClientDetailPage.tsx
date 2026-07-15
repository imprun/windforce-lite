import { Lock, Pencil, Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { DefinitionList, EmptyState, ErrorNotice, Loading, Modal, Panel } from "../components/ui";
import { ClientDialog } from "../features/ClientDialog";
import { InputConfigDialog } from "../features/InputConfigDialog";
import { type InputConfig } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, formatTime } from "../lib/format";
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
      const [client, configs, audit, apps] = await Promise.all([
        api.client(clientID),
        api.clientInputConfigs(clientID),
        api.clientInputConfigAudit(clientID),
        api.apps(),
      ]);
      return { client, configs, audit, apps: apps.apps || [] };
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

  const { client, configs, audit, apps } = state.data;
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
          <div className="tableWrap">
            <table className="table inputSettingsTable" id="clientInputSettings">
              <thead>
                <tr>
                  <th>App</th>
                  <th>Action scope</th>
                  <th>Keys</th>
                  <th>Locked</th>
                  <th>Updated</th>
                  <th>Actor</th>
                  <th aria-label="Row actions" />
                </tr>
              </thead>
              <tbody>
                {configs.map((config) => (
                  <tr key={`${config.app_key}-${config.action_key || "all"}`}>
                    <td>
                      <span className="cellTitle mono">{config.app_key}</span>
                      <span className="cellSub">{appsByKey.has(config.app_key) ? "released" : "release unavailable"}</span>
                    </td>
                    <td>
                      <span className="cellTitle mono">{config.action_key || "All actions"}</span>
                      <span className="cellSub">{config.action_key ? "action override" : "app override"}</span>
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
                        disabled={!appsByKey.has(config.app_key)}
                        onClick={() => setEditingConfig({ appKey: config.app_key, config })}
                      >
                        <Pencil size={15} aria-hidden="true" />
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <Panel title="Input settings audit" subtitle="Who changed client-specific input settings, and when.">
        {audit.length === 0 ? (
          <EmptyState title="No input-setting changes recorded yet." />
        ) : (
          <div className="tableWrap">
            <table className="table" id="clientInputSettingsAudit">
              <thead>
                <tr>
                  <th>When</th>
                  <th>Actor</th>
                  <th>App</th>
                  <th>Action</th>
                  <th>Change</th>
                  <th>Summary</th>
                </tr>
              </thead>
              <tbody>
                {audit.map((record) => (
                  <tr key={record.id}>
                    <td title={formatTime(record.created_at)}>{formatRelative(record.created_at)}</td>
                    <td>{record.actor}</td>
                    <td className="mono">{record.app_key}</td>
                    <td className="mono">{record.action_key || "all actions"}</td>
                    <td>{record.kind}</td>
                    <td className="mono">{record.detail || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
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
