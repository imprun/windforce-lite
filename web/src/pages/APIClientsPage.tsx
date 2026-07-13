import { useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { EmptyState, ErrorNotice, Loading } from "../components/ui";
import { APIClientDialog } from "../features/APIClientDialog";
import type { APIClient } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, formatTime } from "../lib/format";

export function APIClientsPage() {
  const { api } = useApp();
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<APIClient | "new" | null>(null);
  const state = useAsync(() => api.apiClients(), [api]);

  const clients = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!state.data || !query) return state.data || [];
    return state.data.filter(
      (client) => client.name.toLowerCase().includes(query) || client.client_key.toLowerCase().includes(query),
    );
  }, [search, state.data]);

  function finishChange() {
    setEditing(null);
    state.reload();
  }

  return (
    <Layout
      title="API Clients"
      subtitle="Stable client identities used by app and action configuration."
      actions={
        <>
          <input
            className="searchInput"
            placeholder="Filter API clients…"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            aria-label="Filter API clients"
          />
          <button className="button" type="button" onClick={() => state.reload()}>
            Refresh
          </button>
          <button className="button primary" type="button" onClick={() => setEditing("new")}>
            Create API Client
          </button>
        </>
      }
    >
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading /> : null}
      {state.data ? (
        clients.length === 0 ? (
          <EmptyState title={search ? "No API clients match the filter." : "No API clients registered yet."} />
        ) : (
          <div className="tableWrap">
            <table className="table" id="apiClientList">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Client key</th>
                  <th>Updated</th>
                  <th>Updated by</th>
                  <th aria-label="Row actions" />
                </tr>
              </thead>
              <tbody>
                {clients.map((client) => (
                  <tr key={client.id}>
                    <td>
                      <span className="cellTitle">{client.name}</span>
                    </td>
                    <td className="mono">{client.client_key}</td>
                    <td title={formatTime(client.updated_at)}>
                      <span className="cellTitle">{formatRelative(client.updated_at)}</span>
                      <span className="cellSub">{formatTime(client.updated_at)}</span>
                    </td>
                    <td>{client.updated_by}</td>
                    <td className="rowActions">
                      <button className="button small" type="button" onClick={() => setEditing(client)}>
                        Edit
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      ) : null}

      {editing ? (
        <APIClientDialog
          client={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={finishChange}
          onDeleted={finishChange}
        />
      ) : null}
    </Layout>
  );
}
