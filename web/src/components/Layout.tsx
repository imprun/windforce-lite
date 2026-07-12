import { useEffect, useState, type ReactNode } from "react";
import { useApp } from "../lib/app-context";
import { Link, useRouter } from "../lib/router";

const navItems = [
  { to: "/", label: "Apps", icon: "▤", match: (path: string) => path === "/" || path.startsWith("/apps") },
  { to: "/monitoring", label: "Monitoring", icon: "◔", match: (path: string) => path.startsWith("/monitoring") || path.startsWith("/jobs") },
  { to: "/settings", label: "Settings", icon: "⚙", match: (path: string) => path.startsWith("/settings") },
];

function loadCollapsed(): boolean {
  return globalThis.localStorage?.getItem("wf.sidebarCollapsed") === "true";
}

export function Layout({
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
  const { path } = useRouter();
  const { settings, toasts, dismissToast } = useApp();
  const [collapsed, setCollapsed] = useState(loadCollapsed);

  useEffect(() => {
    globalThis.localStorage?.setItem("wf.sidebarCollapsed", String(collapsed));
  }, [collapsed]);

  return (
    <div className={collapsed ? "appShell sidebarCollapsed" : "appShell"}>
      <aside className="sidebar">
        <Link className="brand" to="/" title="windforce-lite">
          <span className="brandMark" aria-hidden="true">
            ⌁
          </span>
          <span className="brandName">windforce-lite</span>
        </Link>
        <nav className="nav" aria-label="Primary">
          {navItems.map((item) => (
            <Link
              key={item.to}
              to={item.to}
              className={item.match(path) ? "navItem active" : "navItem"}
              title={item.label}
            >
              <span className="navIcon" aria-hidden="true">
                {item.icon}
              </span>
              <span className="navLabel">{item.label}</span>
            </Link>
          ))}
        </nav>
        <button
          id="sidebarToggle"
          type="button"
          className="sidebarToggle"
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-expanded={!collapsed}
          onClick={() => setCollapsed((current) => !current)}
        >
          <span aria-hidden="true">{collapsed ? "»" : "«"}</span>
          <span className="navLabel">Collapse</span>
        </button>
        <div className="sidebarFooter">
          <span className="workspacePill" title="Active workspace">
            workspace / {settings.workspace}
          </span>
          <span className="actorPill" title="Audit actor for state-changing requests">
            actor / {settings.actor || "system"}
          </span>
        </div>
      </aside>
      <div className="mainArea">
        <header className="topbar">
          <div>
            <h1>{title}</h1>
            {subtitle ? <p className="topbarSubtitle">{subtitle}</p> : null}
          </div>
          {actions ? <div className="topbarActions">{actions}</div> : null}
        </header>
        <main className="content">{children}</main>
      </div>
      <div className="toastStack" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={`toast toast-${toast.tone}`} id="toast">
            <span>{toast.text}</span>
            <button type="button" className="toastClose" aria-label="Dismiss" onClick={() => dismissToast(toast.id)}>
              ×
            </button>
          </div>
        ))}
      </div>
    </div>
  );
}
