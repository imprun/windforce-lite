import { EmptyState } from "../components/ui";
import type { AuditChanges, AuditEvent } from "../lib/api";
import { formatRelative, formatTime } from "../lib/format";
import { Link } from "../lib/router";

const categoryLabels: Record<string, string> = {
  repository: "Repository",
  release: "Release",
  client: "Client Registry",
  input_settings: "Input Settings",
  webhook: "Webhook",
};

const changeLabels: Array<[keyof AuditChanges, string]> = [
  ["added", "Added"],
  ["updated", "Updated"],
  ["removed", "Removed"],
  ["locked", "Locked"],
  ["unlocked", "Unlocked"],
];

export function auditChangeGroups(changes?: AuditChanges): Array<{ label: string; keys: string[] }> {
  if (!changes) return [];
  return changeLabels.flatMap(([key, label]) => {
    const keys = changes[key] || [];
    return keys.length ? [{ label, keys }] : [];
  });
}

function AuditScope({ event }: { event: AuditEvent }) {
  return (
    <div className="auditScope">
      {event.app_key ? (
        event.git_source_id ? (
          <Link className="cellTitle mono" to={`/apps/${event.git_source_id}/audit`}>
            {event.app_key}
          </Link>
        ) : (
          <span className="cellTitle mono">{event.app_key}</span>
        )
      ) : null}
      {event.client_id ? (
        <Link className={event.app_key ? "cellSub" : "cellTitle"} to={`/clients/${event.client_id}`}>
          {event.client_name || "Registered client"}
        </Link>
      ) : null}
      {event.action_key ? <span className="cellSub mono">Action {event.action_key}</span> : null}
      {event.webhook_subscription_id ? (
        <Link
          className={event.app_key || event.client_id ? "cellSub" : "cellTitle"}
          to={`/settings/webhooks/${event.webhook_subscription_id}/audit`}
        >
          Webhook {event.webhook_subscription_id.slice(0, 12)}…
        </Link>
      ) : null}
      {!event.app_key && !event.client_id && !event.action_key && !event.webhook_subscription_id && event.git_source_id ? (
        <span className="cellTitle">Repository source #{event.git_source_id}</span>
      ) : null}
      {!event.app_key && !event.client_id && !event.action_key && !event.git_source_id && !event.webhook_subscription_id ? (
        <span className="cellSub">Workspace</span>
      ) : null}
    </div>
  );
}

function AuditDetail({ event }: { event: AuditEvent }) {
  const groups = auditChangeGroups(event.changes);
  if (groups.length) {
    return (
      <div className="auditChanges">
        {groups.map((group) => (
          <span key={group.label}>
            <strong>{group.label}</strong> <code>{group.keys.join(", ")}</code>
          </span>
        ))}
      </div>
    );
  }
  return <span className={event.detail ? "auditDetail" : "cellSub"}>{event.detail || "No additional detail"}</span>;
}

export function AuditEventTable({ events, emptyTitle = "No audit events match this view." }: { events: AuditEvent[]; emptyTitle?: string }) {
  if (events.length === 0) return <EmptyState title={emptyTitle} />;

  return (
    <div className="tableWrap auditTableWrap">
      <table className="table auditTable" id="auditEvents">
        <thead>
          <tr>
            <th>When</th>
            <th>Actor</th>
            <th>Category</th>
            <th>Change</th>
            <th>Scope</th>
            <th>Detail</th>
          </tr>
        </thead>
        <tbody>
          {events.map((event) => (
            <tr key={`${event.category}-${event.id}`}>
              <td title={formatTime(event.created_at)}>
                <span className="cellTitle">{formatRelative(event.created_at)}</span>
                <span className="cellSub">{formatTime(event.created_at)}</span>
              </td>
              <td>{event.actor || "system"}</td>
              <td><span className="badge auditCategory">{categoryLabels[event.category] || event.category}</span></td>
              <td>
                <span className="cellTitle">{event.summary}</span>
                <span className="cellSub mono">{event.kind}</span>
              </td>
              <td><AuditScope event={event} /></td>
              <td><AuditDetail event={event} /></td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
