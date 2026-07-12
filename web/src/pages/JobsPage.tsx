import { useState } from "react";
import { Layout } from "../components/Layout";
import { EmptyState, ErrorNotice, Loading, Panel } from "../components/ui";
import type { JobStatusCounts } from "../lib/api";
import { useApp, useAsync } from "../lib/app-context";
import { formatRelative } from "../lib/format";
import { Link } from "../lib/router";

const windows = [
  { label: "1h", seconds: 3600 },
  { label: "24h", seconds: 86400 },
  { label: "7d", seconds: 604800 },
] as const;

export function JobsPage() {
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
  const windowLabel = windows.find((item) => item.seconds === windowSeconds)?.label || "24h";
  const sourceByApp = new Map((state.data?.apps || []).map((app) => [app.app_key, app.git_source_id]));

  return (
    <Layout
      title="Jobs"
      subtitle="Aggregate run activity across the workspace. Individual runs live in the control-plane API and CLI."
      actions={
        <>
          <div className="segmented" role="group" aria-label="Recent window">
            {windows.map((item) => (
              <button
                key={item.label}
                type="button"
                className={item.seconds === windowSeconds ? "segment active" : "segment"}
                onClick={() => setWindowSeconds(item.seconds)}
              >
                {item.label}
              </button>
            ))}
          </div>
          <button className="button" type="button" onClick={() => state.reload()}>
            Refresh
          </button>
        </>
      }
    >
      {state.error ? <ErrorNotice message={state.error} onRetry={state.reload} /> : null}
      {state.loading && !summary ? <Loading /> : null}

      {summary ? (
        <>
          <div className="statRow" id="jobSummary">
            <StatTile label="Queued" value={summary.queued_count} tone="waiting" />
            <StatTile label="Running" value={summary.running_count} tone="running" />
            <StatTile label={`Completed · ${windowLabel}`} value={summary.completed_count_recent} tone="good" />
            <StatTile label={`Failed · ${windowLabel}`} value={summary.failed_count_recent} tone="critical" />
            <StatTile label={`Canceled · ${windowLabel}`} value={summary.canceled_count_recent} tone="serious" />
          </div>

          {summary.oldest_queued_at ? (
            <div className="inlineNotice">
              Oldest queued job has been waiting since {formatRelative(summary.oldest_queued_at)}.
            </div>
          ) : null}

          <Panel title="By app" subtitle={`Job activity per app over the last ${windowLabel}.`}>
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

          <Panel title="By route tag" subtitle={`Job activity per worker route tag over the last ${windowLabel}.`}>
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
            <th>Queued</th>
            <th>Running</th>
            <th>Completed</th>
            <th>Failed</th>
            <th>Canceled</th>
            <th>Failure rate</th>
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

function StatTile({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | undefined;
  tone: "waiting" | "running" | "good" | "critical" | "serious";
}) {
  return (
    <div className="statTile">
      <span className={`statDot dot-${tone}`} aria-hidden="true" />
      <div>
        <p className="statValue">{value ?? "—"}</p>
        <p className="statLabel">{label}</p>
      </div>
    </div>
  );
}
