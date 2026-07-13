import { useState } from "react";
import { Layout } from "../components/Layout";
import { StatTile, WindowSelector, windowLabel } from "../components/stats";
import { EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import type { JobStatusCounts } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative } from "../lib/format";
import { Link } from "../lib/router";

export function MonitoringPage({ legacyJobID }: { legacyJobID?: string } = {}) {
  const { api } = useApp();
  const [windowSeconds, setWindowSeconds] = useState<number>(86400);

  const state = useAsync(
    async () => {
      const [summary, apps] = await Promise.all([api.jobsSummary(windowSeconds), api.apps()]);
      return { summary, apps: apps.apps || [] };
    },
    [api, windowSeconds],
  );
  const summary = state.data?.summary || null;
  const label = windowLabel(windowSeconds);
  const sourceByApp = new Map((state.data?.apps || []).map((app) => [app.app_key, app.git_source_id]));

  return (
    <Layout
      title="Monitoring"
      subtitle="Aggregate job activity across the workspace. Individual runs live in the control-plane API and CLI."
      actions={
        <>
          <WindowSelector value={windowSeconds} onChange={setWindowSeconds} />
          <button className="button" type="button" onClick={() => state.reload()}>
            Refresh
          </button>
        </>
      }
    >
      {legacyJobID ? (
        <div className="inlineNotice">
          The Web UI shows aggregate job activity only. Individual runs such as job{" "}
          <span className="mono">{legacyJobID}</span> are available through the control-plane API and CLI.
        </div>
      ) : null}
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !summary ? <Loading /> : null}

      {summary ? (
        <>
          <div className="statRow" id="jobSummary">
            <StatTile label="Queued" value={summary.queued_count} tone="waiting" />
            <StatTile label="Running" value={summary.running_count} tone="running" />
            <StatTile label={`Completed · ${label}`} value={summary.completed_count_recent} tone="good" />
            <StatTile label={`Failed · ${label}`} value={summary.failed_count_recent} tone="critical" />
            <StatTile label={`Canceled · ${label}`} value={summary.canceled_count_recent} tone="serious" />
          </div>

          {summary.oldest_queued_at ? (
            <div className="inlineNotice">
              Oldest queued job has been waiting since {formatRelative(summary.oldest_queued_at)}.
            </div>
          ) : null}

          <Panel title="By app" subtitle={`Job activity per app over the last ${label}.`}>
            <BreakdownTable
              id="jobsByApp"
              nameHeader="App"
              rows={(summary.by_app || []).map((item) => ({
                key: item.app_key,
                name: item.app_key,
                sourceID: sourceByApp.get(item.app_key),
                counts: item,
              }))}
            />
          </Panel>

          <Panel title="By route tag" subtitle={`Job activity per worker route tag over the last ${label}.`}>
            <BreakdownTable
              id="jobsByTag"
              nameHeader="Route tag"
              rows={(summary.by_tag || []).map((item) => ({
                key: item.tag,
                name: item.tag,
                counts: item,
              }))}
            />
          </Panel>
        </>
      ) : null}
    </Layout>
  );
}

type BreakdownRow = {
  key: string;
  name: string;
  sourceID?: number;
  counts: JobStatusCounts;
};

function BreakdownTable({ id, nameHeader, rows }: { id: string; nameHeader: string; rows: BreakdownRow[] }) {
  if (rows.length === 0) {
    return <EmptyState title="No job activity in this window." />;
  }
  return (
    <div className="tableWrap">
      <table className="table" id={id}>
        <thead>
          <tr>
            <th>{nameHeader}</th>
            <th className="numCell">Queued</th>
            <th className="numCell">Running</th>
            <th className="numCell">Completed</th>
            <th className="numCell">Failed</th>
            <th className="numCell">Canceled</th>
            <th className="numCell">Failure rate</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr key={row.key} className="tableRow">
              <td>
                {row.sourceID ? (
                  <Link to={`/apps/${row.sourceID}`} className="cellTitle mono">
                    {row.name}
                  </Link>
                ) : (
                  <span className="cellTitle mono">{row.name}</span>
                )}
              </td>
              <td className="numCell">{row.counts.queued_count}</td>
              <td className="numCell">{row.counts.running_count}</td>
              <td className="numCell">{row.counts.completed_count_recent}</td>
              <td className="numCell">{row.counts.failed_count_recent}</td>
              <td className="numCell">{row.counts.canceled_count_recent}</td>
              <td className="numCell">
                <FailureRate counts={row.counts} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function FailureRate({ counts }: { counts: JobStatusCounts }) {
  const settled = counts.completed_count_recent + counts.failed_count_recent;
  if (settled === 0) return <span>—</span>;
  const rate = (counts.failed_count_recent / settled) * 100;
  const label = `${rate.toFixed(rate > 0 && rate < 1 ? 1 : 0)}%`;
  return <span className={rate > 0 ? "failureRate bad" : "failureRate"}>{label}</span>;
}
