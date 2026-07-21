import { useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { DefinitionList, EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import { AuditEventTable } from "../features/AuditEventTable";
import { ClientDialog } from "../features/ClientDialog";
import { ClientInputSettings } from "../features/ClientInputSettings";
import type { Client, InputConfig } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, formatTime } from "../lib/format";
import { groupInputSettings } from "../lib/input-setting-groups";
import { Link, useRouter } from "../lib/router";

const tabs = [
  { key: "overview", label: "Overview" },
  { key: "input-settings", label: "Input Settings" },
  { key: "audit", label: "Audit" },
] as const;

type TabKey = (typeof tabs)[number]["key"];

export function ClientDetailPage({
  clientID,
  tab,
  appKey,
}: {
  clientID: string;
  tab: string;
  appKey?: string;
}) {
  const { api } = useApp();
  const { navigate } = useRouter();
  const [editingClient, setEditingClient] = useState(false);
  const activeTab = (tabs.find((item) => item.key === tab)?.key || "overview") as TabKey;
  const state = useAsync(async () => {
    const [client, configs, apps] = await Promise.all([
      api.client(clientID),
      api.clientInputConfigs(clientID),
      api.apps(),
    ]);
    return { client, configs, apps: apps.apps || [] };
  }, [api, clientID]);

  if (state.loading && !state.data)
    return (
      <Layout title="Client Registry">
        <Loading />
      </Layout>
    );
  if (state.error || !state.data) {
    return (
      <Layout title="Client not found">
        <ErrorNotice message={state.error || "Client not found."} onRetry={state.reload} />
      </Layout>
    );
  }

  const { client, configs, apps } = state.data;
  return (
    <Layout
      title={client.name}
      subtitle="External client configuration across released apps."
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
      <nav className="tabBar" aria-label="Client detail tabs">
        {tabs.map((item) => (
          <Link
            key={item.key}
            className={item.key === activeTab ? "tab active" : "tab"}
            to={
              item.key === "overview"
                ? `/clients/${client.id}`
                : `/clients/${client.id}/${item.key}`
            }
          >
            {item.label}
          </Link>
        ))}
      </nav>

      {activeTab === "overview" ? <ClientOverview client={client} configs={configs} /> : null}
      {activeTab === "input-settings" ? (
        <ClientInputSettings
          client={client}
          configs={configs}
          apps={apps}
          selectedAppKey={appKey}
          onChanged={state.reload}
        />
      ) : null}
      {activeTab === "audit" ? <ClientAudit clientID={client.id} /> : null}

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
    </Layout>
  );
}

function ClientOverview({ client, configs }: { client: Client; configs: InputConfig[] }) {
  const groups = useMemo(() => groupInputSettings(configs, (config) => config.app_key), [configs]);
  const latest = useMemo(
    () =>
      configs.reduce(
        (current, config) =>
          !current || Date.parse(config.updated_at) > Date.parse(current.updated_at)
            ? config
            : current,
        undefined as (typeof configs)[number] | undefined,
      ),
    [configs],
  );
  return (
    <>
      <Panel
        title="Client identity"
        subtitle="Used by public API calls to select client-specific settings."
      >
        <DefinitionList
          items={[
            ["Name", client.name],
            ["API token", client.has_token ? "Active" : "Not issued"],
            ["Updated", formatTime(client.updated_at)],
            ["Updated by", client.updated_by],
          ]}
        />
      </Panel>
      <Panel
        title="Configuration summary"
        subtitle="Current client-specific coverage across released apps."
      >
        {configs.length ? (
          <DefinitionList
            items={[
              ["Configured apps", groups.length],
              ["Action scopes", configs.length],
              ["Configured values", groups.reduce((total, group) => total + group.valueCount, 0)],
              ["Locked values", groups.reduce((total, group) => total + group.lockedCount, 0)],
              [
                "Last settings change",
                latest ? `${formatRelative(latest.updated_at)} · ${latest.updated_by}` : "—",
              ],
            ]}
          />
        ) : (
          <EmptyState title="No client-specific input settings." />
        )}
      </Panel>
    </>
  );
}

function ClientAudit({ clientID }: { clientID: string }) {
  const { api } = useApp();
  const state = useAsync(() => api.auditEvents({ clientID, limit: 250 }), [api, clientID]);
  return (
    <Panel title="Audit trail" subtitle="Registry and input-setting changes for this client.">
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading /> : null}
      {state.data ? (
        <AuditEventTable
          events={state.data}
          emptyTitle="No changes have been recorded for this client."
        />
      ) : null}
    </Panel>
  );
}
