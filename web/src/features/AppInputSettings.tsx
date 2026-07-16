import { Plus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import { type AppDetail, type InputConfig } from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp, useAsync } from "../lib/app-context";
import { groupInputSettings, inputSettingGroupMatches, paginate } from "../lib/input-setting-groups";
import { Link } from "../lib/router";
import { InputConfigDialog } from "./InputConfigDialog";
import { InputSettingScopeList } from "./InputSettingScopeList";
import { countLabel, InputSettingSummaryTable, SummaryPagination } from "./InputSettingSummaryTable";

const PAGE_SIZE = 25;

export function AppInputSettings({
  detail,
  sourceID,
  selectedClientID,
}: {
  detail: AppDetail;
  sourceID: number;
  selectedClientID?: string;
}) {
  const { api } = useApp();
  const [editing, setEditing] = useState<InputConfig | "new" | null>(null);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(1);
  const state = useAsync(
    async () => {
      const [configs, clients] = await Promise.all([api.appInputConfigs(detail.app.app_key), api.clients()]);
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
  const groups = useMemo(() => {
    const grouped = groupInputSettings(state.data?.configs || [], (config) => config.client_id || "");
    return grouped.sort((left, right) => {
      if (!left.key) return -1;
      if (!right.key) return 1;
      const leftName = clientsByID.get(left.key)?.name || left.key;
      const rightName = clientsByID.get(right.key)?.name || right.key;
      return leftName.localeCompare(rightName);
    });
  }, [clientsByID, state.data?.configs]);
  const filteredGroups = useMemo(() => groups.filter((group) => {
    const actionNames = group.actionKeys.flatMap((actionKey) => {
      const action = actionsByKey.get(actionKey);
      return action ? [actionDisplayName(action.display_name) || action.action_key] : [];
    });
    return inputSettingGroupMatches(group, search, [clientsByID.get(group.key)?.name || "App default", ...actionNames]);
  }), [actionsByKey, clientsByID, groups, search]);
  const pagedGroups = useMemo(() => paginate(filteredGroups, page, PAGE_SIZE), [filteredGroups, page]);

  useEffect(() => setPage(1), [search]);
  useEffect(() => {
    if (page !== pagedGroups.page) setPage(pagedGroups.page);
  }, [page, pagedGroups.page]);

  function finish() {
    setEditing(null);
    state.reload();
  }

  const selectedScopeKey = selectedClientID === "default" ? "" : selectedClientID;
  const selectedGroup = selectedClientID === undefined ? undefined : groups.find((group) => group.key === selectedScopeKey);
  const selectedClient = selectedScopeKey ? clientsByID.get(selectedScopeKey) : undefined;
  const selectedLabel = selectedScopeKey ? selectedClient?.name || "Removed client" : "All clients (app default)";
  const fixedClientID = selectedClientID === undefined ? undefined : selectedScopeKey;

  return (
    <>
      {selectedClientID === undefined ? (
        <Panel
          title="Input settings"
          subtitle="Client scopes with configured values. Open a scope to inspect and edit its action-level settings."
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
          {state.data ? (
            state.data.configs.length === 0 ? (
              <EmptyState title="No input settings for this app." />
            ) : (
              <>
                <div className="settingsSummaryToolbar">
                  <input
                    className="searchInput"
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder="Search clients, actions, or setting keys…"
                    aria-label="Search client input settings"
                  />
                  <span>{countLabel(filteredGroups.length, "client scope")}</span>
                </div>
                {filteredGroups.length ? (
                  <InputSettingSummaryTable
                    id="appInputSettingsSummary"
                    scopeHeading="Client scope"
                    rows={pagedGroups.items.map((group) => {
                      const client = group.key ? clientsByID.get(group.key) : undefined;
                      const label = group.key ? client?.name || "Removed client" : "All clients";
                      const actionNames = group.actionKeys.map((actionKey) => {
                        if (!actionKey) return "All actions";
                        const action = actionsByKey.get(actionKey);
                        return action ? actionDisplayName(action.display_name) || actionKey : actionKey;
                      });
                      return {
                        group,
                        label,
                        subtitle: group.key ? "Client override" : "App default",
                        href: `/apps/${sourceID}/input-settings/client/${group.key || "default"}`,
                        coverage: countLabel(group.configs.length, "action scope"),
                        coverageDetail: actionNames.join(", "),
                      };
                    })}
                  />
                ) : (
                  <EmptyState title="No client settings match the search." />
                )}
                <SummaryPagination
                  page={pagedGroups.page}
                  totalPages={pagedGroups.totalPages}
                  totalItems={filteredGroups.length}
                  pageSize={PAGE_SIZE}
                  onChange={setPage}
                />
              </>
            )
          ) : null}
        </Panel>
      ) : (
        <Panel
          title={`${selectedLabel} settings`}
          subtitle="Action-level values applied to this client scope before execution."
          actions={
            <>
              <Link className="button" to={`/apps/${sourceID}/input-settings`}>Back to client scopes</Link>
              <button className="button primary" type="button" onClick={() => setEditing("new")}>
                <Plus size={16} aria-hidden="true" />
                Add settings
              </button>
            </>
          }
        >
          {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
          {state.loading && !state.data ? <Loading /> : null}
          {state.data && !selectedGroup && selectedScopeKey && !selectedClient ? (
            <EmptyState title="This client scope is not available." />
          ) : null}
          {state.data && !selectedGroup && (!selectedScopeKey || selectedClient) ? (
            <EmptyState title="No input settings in this client scope." />
          ) : null}
          {selectedGroup ? (
            <InputSettingScopeList
              id="appInputSettings"
              items={selectedGroup.configs.map((config) => {
                const action = config.action_key ? actionsByKey.get(config.action_key) : undefined;
                const actionName = action ? actionDisplayName(action.display_name) || action.action_key : "All actions";
                return {
                  key: `${config.client_id || "default"}-${config.action_key || "all"}`,
                  config,
                  primaryLabel: "Client scope",
                  primaryValue: selectedClient ? (
                    <Link to={`/clients/${selectedClient.id}/input-settings/${detail.app.app_key}`}>{selectedClient.name}</Link>
                  ) : "All clients",
                  primaryMeta: selectedClient ? "Client override" : "App default",
                  actionName,
                  actionMeta: config.action_key || "App default",
                  editLabel: `Edit input settings for ${selectedLabel}, ${actionName}`,
                  onEdit: () => setEditing(config),
                };
              })}
            />
          ) : null}
        </Panel>
      )}

      {editing && state.data ? (
        <InputConfigDialog
          appKey={detail.app.app_key}
          actions={detail.actions}
          clients={state.data.clients}
          existing={editing === "new" ? undefined : editing}
          fixedClientID={fixedClientID}
          onClose={() => setEditing(null)}
          onSaved={finish}
        />
      ) : null}
    </>
  );
}
