import { useEffect, type ReactNode } from "react";
import type { ProbeResult } from "../lib/api";
import { formatJSON } from "../lib/format";

export function ReleaseStateBadge({ released }: { released: boolean }) {
  return released ? (
    <span className="badge badge-good">
      <span aria-hidden="true" className="badgeIcon">
        ✓
      </span>
      released
    </span>
  ) : (
    <span className="badge badge-neutral">
      <span aria-hidden="true" className="badgeIcon">
        ○
      </span>
      registered
    </span>
  );
}

export function Panel({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="panel">
      <header className="panelHeader">
        <div>
          <h2>{title}</h2>
          {subtitle ? <p className="panelSubtitle">{subtitle}</p> : null}
        </div>
        {actions ? <div className="panelActions">{actions}</div> : null}
      </header>
      <div className="panelBody">{children}</div>
    </section>
  );
}

export function DefinitionList({ items }: { items: Array<[string, ReactNode]> }) {
  return (
    <dl className="defList">
      {items.map(([label, value]) => (
        <div className="defItem" key={label}>
          <dt>{label}</dt>
          <dd>{value ?? "—"}</dd>
        </div>
      ))}
    </dl>
  );
}

export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="emptyState">
      <p className="emptyTitle">{title}</p>
      {children ? <div className="emptyBody">{children}</div> : null}
    </div>
  );
}

export function ErrorNotice({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="errorNotice" role="alert">
      <span>{message}</span>
      {onRetry ? (
        <button className="button small" type="button" onClick={onRetry}>
          Retry
        </button>
      ) : null}
    </div>
  );
}

export function Loading({ label = "Loading…" }: { label?: string }) {
  return <p className="loading">{label}</p>;
}

export function JsonBlock({ value, maxHeight }: { value: unknown; maxHeight?: number }) {
  const text = typeof value === "string" ? value : formatJSON(value);
  return (
    <pre className="codeBlock" style={maxHeight ? { maxHeight } : undefined}>
      {text || "(empty)"}
    </pre>
  );
}

export function Modal({
  title,
  subtitle,
  onClose,
  children,
  id,
  wide,
}: {
  title: string;
  subtitle?: string;
  onClose: () => void;
  children: ReactNode;
  id?: string;
  wide?: boolean;
}) {
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, [onClose]);

  return (
    <div className="modalBackdrop" role="presentation" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <section className={wide ? "dialog wide" : "dialog"} role="dialog" aria-modal="true" aria-label={title} id={id}>
        <header className="dialogHeader">
          <div>
            <h2>{title}</h2>
            {subtitle ? <p>{subtitle}</p> : null}
          </div>
          <button className="button small" type="button" onClick={onClose}>
            Close
          </button>
        </header>
        <div className="dialogBody">{children}</div>
      </section>
    </div>
  );
}

export function ProbeNotice({ probe, branch }: { probe: ProbeResult; branch: string }) {
  if (!probe.reachable) {
    return <div className="inlineNotice error">{probe.error || "Repository is not reachable."}</div>;
  }
  const branchName = probe.branch || branch;
  const branches = probe.branches?.length ? ` Remote branches: ${probe.branches.slice(0, 8).join(", ")}.` : "";
  return (
    <div className="inlineNotice ok">
      Repository reachable. Branch {branchName} {probe.branch_exists ? "exists" : "was not found"}.{branches}
    </div>
  );
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <label className="field">
      <span className="fieldLabel">{label}</span>
      {children}
      {hint ? <span className="fieldHint">{hint}</span> : null}
    </label>
  );
}
