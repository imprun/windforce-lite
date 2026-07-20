import { useEffect, useState, type ReactNode } from "react";
import {
  Activity,
  AppWindow,
  ArrowLeft,
  ContactRound,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Settings,
  Wind,
} from "lucide-react";
import { useApp } from "../lib/app-context";
import { Link, useRouter } from "../lib/router";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";

export const primaryNavItems = [
  { to: "/", label: "Apps", icon: AppWindow, match: (path: string) => path === "/" || path.startsWith("/apps") },
  { to: "/clients", label: "Client Registry", icon: ContactRound, match: (path: string) => path.startsWith("/clients") },
  { to: "/monitoring", label: "Monitoring", icon: Activity, match: (path: string) => path.startsWith("/monitoring") || path.startsWith("/jobs") },
  { to: "/audit", label: "Audit", icon: ScrollText, match: (path: string) => path.startsWith("/audit") },
  { to: "/settings", label: "Settings", icon: Settings, match: (path: string) => path.startsWith("/settings") && path !== "/settings/workspaces" },
];

function loadCollapsed(): boolean {
  return globalThis.localStorage?.getItem("wf.sidebarCollapsed") === "true";
}

export function Layout({
  title,
  subtitle,
  actions,
  children,
  scope = "workspace",
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
  scope?: "workspace" | "instance";
}) {
  const { path } = useRouter();
  const { settings, toasts, dismissToast } = useApp();
  const [collapsed, setCollapsed] = useState(loadCollapsed);

  useEffect(() => {
    globalThis.localStorage?.setItem("wf.sidebarCollapsed", String(collapsed));
  }, [collapsed]);

  return (
    <div className={scope === "instance" ? "appShell instanceAdminShell" : collapsed ? "appShell sidebarCollapsed" : "appShell"}>
      {scope === "workspace" ? <aside className="sidebar">
        <div className="sidebarHeader">
          <Link className="brand" to="/" title="windforce-core">
            <span className="brandMark" aria-hidden="true">
              <Wind size={17} strokeWidth={2.2} />
            </span>
            <span className="brandName">windforce-core</span>
          </Link>
          <button
            id="sidebarToggle"
            type="button"
            className="sidebarToggle"
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-expanded={!collapsed}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            onClick={() => setCollapsed((current) => !current)}
          >
            {collapsed ? (
              <PanelLeftOpen size={18} strokeWidth={1.8} aria-hidden="true" />
            ) : (
              <PanelLeftClose size={18} strokeWidth={1.8} aria-hidden="true" />
            )}
          </button>
        </div>
        <nav className="nav" aria-label="Primary">
          {primaryNavItems.map((item) => {
            const Icon = item.icon;
            return (
              <Link
                key={item.to}
                to={item.to}
                className={item.match(path) ? "navItem active" : "navItem"}
                title={item.label}
              >
                <span className="navIcon" aria-hidden="true">
                  <Icon size={18} strokeWidth={1.8} />
                </span>
                <span className="navLabel">{item.label}</span>
              </Link>
            );
          })}
        </nav>
        <div className="sidebarFooter">
          <WorkspaceSwitcher />
          <span className="actorPill" title="Audit actor for state-changing requests">
            actor / {settings.actor || "system"}
          </span>
        </div>
      </aside> : null}
      <div className="mainArea">
        {scope === "instance" ? (
          <header className="instanceAdminBar">
            <div className="instanceAdminIdentity">
              <Link className="brand" to="/" title="windforce-core">
                <span className="brandMark" aria-hidden="true">
                  <Wind size={17} strokeWidth={2.2} />
                </span>
                <span className="brandName">windforce-core</span>
              </Link>
              <span className="instanceAdminDivider" aria-hidden="true" />
              <span className="instanceAdminContext">Instance administration</span>
            </div>
            <Link className="button" to="/">
              <ArrowLeft size={16} aria-hidden="true" /> Back to workspace
            </Link>
          </header>
        ) : null}
        <header className="topbar">
          <div>
            <h1>{title}</h1>
            {subtitle ? <p className="topbarSubtitle">{subtitle}</p> : null}
            {scope === "workspace" ? <div className="mobileWorkspaceContext"><WorkspaceSwitcher /></div> : null}
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
