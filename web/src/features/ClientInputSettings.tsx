import { Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { EmptyState, ErrorNotice, Loading, Modal, Panel } from "../components/ui";
import { type AppSummary, type Client, type InputConfig } from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp, useAsync } from "../lib/app-context";
import { groupInputSettings, inputSettingGroupMatches } from "../lib/input-setting-groups";
import { Link } from "../lib/router";
import { InputConfigDialog } from "./InputConfigDialog";
import { InputSettingScopeList } from "./InputSettingScopeList";
import { countLabel, InputSettingSummaryTable } from "./InputSettingSummaryTable";

type EditingConfig = { appKey: string; config?: InputConfig };

export function ClientInputSettings({
  client,
  configs,
  apps,
  selectedAppKey,
  onChanged,
}: {
  client: Client;
  configs: InputConfig[];
  apps: AppSummary[];
  selectedAppKey?: string;
  onChanged: () => void;
}) {
  return selectedAppKey ? (
    <ClientAppInputSettingsDetail
      client={client}
      configs={configs}
      apps={apps}
      appKey={selectedAppKey}
      onChanged={onChanged}
    />
  ) : (
    <ClientInputSettingsSummary client={client} configs={configs} apps={apps} onChanged={onChanged} />
  );
}

function ClientInputSettingsSummary({
  client,
  configs,
  apps,
  onChanged,
}: {
  client: Client;
  configs: InputConfig[];
  apps: AppSummary[];
  onChanged: () => void;
}) {
  const [search, setSearch] = useState("");
  const [selectedApp, setSelectedApp] = useState("");
  const [editing, setEditing] = useState<EditingConfig | null>(null);
  const appsByKey = useMemo(() => new Map(apps.map((app) => [app.app_key, app])), [apps]);
  const groups = useMemo(
    () => groupInputSettings(configs, (config) => config.app_key).sort((left, right) => left.key.localeCompare(right.key)),
    [configs],
  );
  const filteredGroups = useMemo(
    () => groups.filter((group) => inputSettingGroupMatches(group, search, [appsByKey.get(group.key)?.app_key || ""])),
    [appsByKey, groups, search],
  );

  useEffect(() => {
    if (!selectedApp && apps.length) setSelectedApp(apps[0].app_key);
  }, [apps, selectedApp]);

  function finish() {
    setEditing(null);
    onChanged();
  }

  return (
    <>
      <Panel
        title="Input settings"
        subtitle="Apps with client-specific values. Open an app to inspect and edit its action-level settings."
        actions={
          apps.length ? (
            <div className="inlineActions">
              <select value={selectedApp} aria-label="App for new input settings" onChange={(event) => setSelectedApp(event.target.value)}>
                {apps.map((app) => <option key={app.app_key} value={app.app_key}>{app.app_key}</option>)}
              </select>
              <button className="button primary" type="button" disabled={!selectedApp} onClick={() => setEditing({ appKey: selectedApp })}>
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
          <>
            <div className="settingsSummaryToolbar">
              <input
                className="searchInput"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder="Search apps, action keys, or setting keys…"
                aria-label="Search app input settings"
              />
              <span>{countLabel(filteredGroups.length, "configured app")}</span>
            </div>
            {filteredGroups.length ? (
              <InputSettingSummaryTable
                id="clientInputSettingsSummary"
                scopeHeading="App"
                rows={filteredGroups.map((group) => {
                  const app = appsByKey.get(group.key);
                  return {
                    group,
                    label: group.key,
                    subtitle: app ? "Released app" : "Release unavailable",
                    href: `/clients/${client.id}/input-settings/${encodeURIComponent(group.key)}`,
                    coverage: countLabel(group.configs.length, "action scope"),
                    coverageDetail: group.actionKeys.map((key) => key || "All actions").join(", "),
                  };
                })}
              />
            ) : (
              <EmptyState title="No app settings match the search." />
            )}
          </>
        )}
      </Panel>

      {editing ? (
        <ClientInputConfigDialog
          client={client}
          appKey={editing.appKey}
          existing={editing.config}
          onClose={() => setEditing(null)}
          onSaved={finish}
        />
      ) : null}
    </>
  );
}

function ClientAppInputSettingsDetail({
  client,
  configs,
  apps,
  appKey,
  onChanged,
}: {
  client: Client;
  configs: InputConfig[];
  apps: AppSummary[];
  appKey: string;
  onChanged: () => void;
}) {
  const { api } = useApp();
  const [editing, setEditing] = useState<EditingConfig | null>(null);
  const app = apps.find((item) => item.app_key === appKey);
  const detail = useAsync(() => app ? api.app(appKey) : Promise.resolve(null), [api, app, appKey]);
  const scopedConfigs = useMemo(() => configs.filter((config) => config.app_key === appKey), [appKey, configs]);
  const actionsByKey = useMemo(
    () => new Map((detail.data?.actions || []).map((action) => [action.action_key, action])),
    [detail.data?.actions],
  );

  function finish() {
    setEditing(null);
    onChanged();
    detail.reload();
  }

  return (
    <>
      <Panel
        title={`${appKey} settings`}
        subtitle={`Action-level values applied when ${client.name} runs this app.`}
        actions={
          <>
            <Link className="button" to={`/clients/${client.id}/input-settings`}>Back to apps</Link>
            {app ? (
              <button className="button primary" type="button" onClick={() => setEditing({ appKey })}>
                <Plus size={16} aria-hidden="true" />
                Add settings
              </button>
            ) : null}
          </>
        }
      >
        {detail.error ? <ErrorNotice message={detail.error} onRetry={detail.reload} /> : null}
        {detail.loading && app && !detail.data ? <Loading /> : null}
        {!app ? <div className="inlineNotice">The active release is unavailable. Existing values are read-only.</div> : null}
        {scopedConfigs.length === 0 ? <EmptyState title="No input settings for this app and client." /> : null}
        {scopedConfigs.length ? (
          <InputSettingScopeList
            id="clientInputSettings"
            items={scopedConfigs.map((config) => {
              const action = config.action_key ? actionsByKey.get(config.action_key) : undefined;
              const actionName = action ? actionDisplayName(action.display_name) || action.action_key : config.action_key || "All actions";
              return {
                key: `${config.app_key}-${config.action_key || "all"}`,
                config,
                primaryLabel: "App",
                primaryValue: app ? (
                  <Link to={`/apps/${app.git_source_id}/input-settings/client/${client.id}`}>{appKey}</Link>
                ) : appKey,
                primaryMeta: app ? "Released app" : "Release unavailable",
                actionName,
                actionMeta: config.action_key ? `${config.action_key} · Action override` : "App-wide client override",
                editLabel: `Edit ${appKey} input settings for ${actionName}`,
                editDisabled: !app,
                onEdit: () => setEditing({ appKey, config }),
              };
            })}
          />
        ) : null}
      </Panel>

      {editing ? (
        <ClientInputConfigDialog
          client={client}
          appKey={editing.appKey}
          existing={editing.config}
          onClose={() => setEditing(null)}
          onSaved={finish}
        />
      ) : null}
    </>
  );
}

function ClientInputConfigDialog({
  client,
  appKey,
  existing,
  onClose,
  onSaved,
}: {
  client: Client;
  appKey: string;
  existing?: InputConfig;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { api } = useApp();
  const state = useAsync(() => api.app(appKey), [api, appKey]);
  if (state.error) {
    return <Modal title="Input Settings" onClose={onClose}><ErrorNotice message={state.error} onRetry={state.reload} /></Modal>;
  }
  if (!state.data) return <Modal title="Input Settings" onClose={onClose}><Loading /></Modal>;
  return (
    <InputConfigDialog
      appKey={appKey}
      actions={state.data.actions}
      clients={[client]}
      existing={existing}
      fixedClientID={client.id}
      onClose={onClose}
      onSaved={onSaved}
    />
  );
}
