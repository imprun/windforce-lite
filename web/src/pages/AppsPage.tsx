import { useMemo, useState } from "react";
import { Layout } from "../components/Layout";
import { EmptyState, ErrorNotice, Loading, ReleaseStateBadge } from "../components/ui";
import { PublishReleaseDialog } from "../features/PublishReleaseDialog";
import { RegisterAppDialog } from "../features/RegisterAppDialog";
import { SourceReleaseActions } from "../features/SourceReleaseActions";
import type { AppSummary, GitSource } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative, shortSHA } from "../lib/format";
import { Link, useRouter } from "../lib/router";

// Either side may be missing: a registered source may not be released yet,
// and a released app's source registration may have been deleted.
type AppRow = {
  source: GitSource | null;
  app: AppSummary | null;
};

export function AppsPage() {
  const { api } = useApp();
  const { navigate } = useRouter();
  const [search, setSearch] = useState("");
  const [registering, setRegistering] = useState(false);
  const [publishing, setPublishing] = useState<GitSource | null>(null);
  const [actionRevision, setActionRevision] = useState(0);

  const state = useAsync(
    async () => {
      const [sources, apps] = await Promise.all([api.gitSources(), api.apps()]);
      return { sources, apps: apps.apps || [] };
    },
    [api],
  );

  const rows = useMemo<AppRow[]>(() => {
    if (!state.data) return [];
    const bySource = new Map<number, AppSummary>();
    for (const app of state.data.apps) bySource.set(app.git_source_id, app);
    const sourceIDs = new Set(state.data.sources.map((source) => source.id));
    const sourceRows = state.data.sources.map((source) => ({ source, app: bySource.get(source.id) || null }));
    const orphanRows = state.data.apps
      .filter((app) => !sourceIDs.has(app.git_source_id))
      .map((app) => ({ source: null, app }));
    const query = search.trim().toLowerCase();
    return [...sourceRows, ...orphanRows].filter((row) => {
      if (!query) return true;
      return (
        (row.source?.name || "").toLowerCase().includes(query) ||
        (row.source?.repo_url || "").toLowerCase().includes(query) ||
        (row.app?.app_key || "").toLowerCase().includes(query)
      );
    });
  }, [state.data, search]);

  return (
    <Layout
      title="Apps"
      subtitle="Register apps, review repository sources, and publish worker-visible releases."
      actions={
        <>
          <input
            className="searchInput"
            placeholder="Filter apps…"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            aria-label="Filter apps"
          />
          <button
            className="button"
            type="button"
            onClick={() => {
              setActionRevision((current) => current + 1);
              state.reload();
            }}
          >
            Refresh
          </button>
          <button className="button primary" type="button" id="registerAppButton" onClick={() => setRegistering(true)}>
            Register App
          </button>
        </>
      }
    >
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !state.data ? <Loading /> : null}

      {state.data ? (
        rows.length === 0 ? (
          <EmptyState title={search ? "No apps match the filter." : "No apps registered yet."}>
            {!search ? (
              <p>
                Register a repository source to create your first app, or create the managed sample app to explore the
                release flow.
              </p>
            ) : null}
          </EmptyState>
        ) : (
          <div className="tableWrap">
            <table className="table" id="appList">
              <thead>
                <tr>
                  <th>App</th>
                  <th>Release state</th>
                  <th>Repository source</th>
                  <th>Last release</th>
                  <th>Actions</th>
                  <th>Route tag</th>
                  <th aria-label="Row actions" />
                </tr>
              </thead>
              <tbody>
                {rows.map(({ source, app }) => {
                  const detailID = source ? source.id : app!.git_source_id;
                  return (
                    <tr
                      key={detailID}
                      className="tableRow clickable"
                      onClick={() => navigate(`/apps/${detailID}`)}
                    >
                      <td>
                        <Link
                          to={`/apps/${detailID}`}
                          className="cellTitle"
                          onClick={(event) => event.stopPropagation()}
                        >
                          {app ? app.app_key : source!.name}
                        </Link>
                        <span className="cellSub">
                          {app
                            ? source
                              ? source.name !== app.app_key
                                ? `source / ${source.name}`
                                : "released"
                              : "repository source removed"
                            : "registered · pending release"}
                        </span>
                      </td>
                      <td>
                        <ReleaseStateBadge released={Boolean(app)} />
                      </td>
                      <td>
                        {source ? (
                          <>
                            <span className="cellTitle mono">{repoLabel(source.repo_url)}</span>
                            <span className="cellSub mono">
                              {source.branch || "main"}
                              {source.subpath ? ` · ${source.subpath}` : ""}
                              {source.last_synced_commit ? ` · synced ${shortSHA(source.last_synced_commit, 8)}` : " · not synced"}
                            </span>
                          </>
                        ) : (
                          <span className="cellSub">repository source removed</span>
                        )}
                      </td>
                      <td>
                        <span className="cellTitle mono">{shortSHA(app?.commit_sha)}</span>
                        <span className="cellSub">{formatRelative(app?.updated_at)}</span>
                      </td>
                      <td>{app ? app.actions_count : "—"}</td>
                      <td>{app ? <span className="mono">{app.effective_route_tag}</span> : "—"}</td>
                      <td className="rowActions" onClick={(event) => event.stopPropagation()}>
                        {source ? (
                          <SourceReleaseActions
                            key={`${source.id}:${actionRevision}`}
                            compact
                            source={source}
                            activeCommit={app?.commit_sha}
                            onSynced={() => state.reload()}
                            onPublish={setPublishing}
                          />
                        ) : null}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )
      ) : null}

      {registering ? (
        <RegisterAppDialog
          onClose={() => setRegistering(false)}
          onRegistered={(created) => {
            setRegistering(false);
            state.reload();
            navigate(`/apps/${created.id}`);
          }}
        />
      ) : null}
      {publishing ? (
        <PublishReleaseDialog
          source={publishing}
          activeCommit={state.data?.apps.find((app) => app.git_source_id === publishing.id)?.commit_sha}
          onClose={() => setPublishing(null)}
          onPublished={() => {
            const id = publishing.id;
            setPublishing(null);
            state.reload();
            navigate(`/apps/${id}/releases`);
          }}
        />
      ) : null}
    </Layout>
  );
}

function repoLabel(repoURL: string): string {
  return repoURL.replace(/^https?:\/\//, "").replace(/\.git$/, "");
}
