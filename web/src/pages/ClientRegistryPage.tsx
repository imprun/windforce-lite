import { useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { EmptyState, ErrorNotice, Loading } from "../components/ui";
import { ClientDialog } from "../features/ClientDialog";
import type { Client } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, formatTime } from "../lib/format";
import { Link } from "../lib/router";

export function ClientRegistryPage() {
  const { api } = useApp();
  const [search, setSearch] = useState("");
  const [editing, setEditing] = useState<Client | "new" | null>(null);
  const state = useAsync(() => api.clients(), [api]);

  const clients = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!state.data || !query) return state.data || [];
    return state.data.filter((client) => client.name.toLowerCase().includes(query));
  }, [search, state.data]);

  function finishChange() {
    setEditing(null);
    state.reload();
  }

  return (
    <Layout
      title="Client Registry"
      subtitle="External clients available for app- and action-specific configuration."
      actions={
        <>
          <input
            className="searchInput"
            placeholder="Filter clients…"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            aria-label="Filter clients"
          />
          <button className="button" type="button" onClick={() => state.reload()}>
            Refresh
          </button>
          <button className="button primary" type="button" onClick={() => setEditing("new")}>
            Register Client
          </button>
        </>
      }
    >
      <div className="inlineNotice">
        Each client can hold one workspace-scoped API token. Raw tokens are shown only when issued
        or rotated.
      </div>
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading /> : null}
      {state.data ? (
        clients.length === 0 ? (
          <EmptyState
            title={search ? "No clients match the filter." : "No clients registered yet."}
          />
        ) : (
          <div className="tableWrap">
            <table className="table" id="clientList">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>API token</th>
                  <th>Updated</th>
                  <th>Updated by</th>
                  <th aria-label="Row actions" />
                </tr>
              </thead>
              <tbody>
                {clients.map((client) => (
                  <tr key={client.id}>
                    <td>
                      <Link className="cellTitle" to={`/clients/${client.id}`}>
                        {client.name}
                      </Link>
                    </td>
                    <td>{client.has_token ? "Active" : "Not issued"}</td>
                    <td title={formatTime(client.updated_at)}>
                      <span className="cellTitle">{formatRelative(client.updated_at)}</span>
                      <span className="cellSub">{formatTime(client.updated_at)}</span>
                    </td>
                    <td>{client.updated_by}</td>
                    <td className="rowActions">
                      <button
                        className="button small"
                        type="button"
                        onClick={() => setEditing(client)}
                      >
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
        <ClientDialog
          client={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={finishChange}
          onDeleted={finishChange}
        />
      ) : null}
    </Layout>
  );
}
