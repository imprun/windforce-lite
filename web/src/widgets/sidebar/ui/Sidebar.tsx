"use client";

import type { ConsoleSection } from "@/views/deployments/ui/types";

type Props = {
  active: ConsoleSection;
  collapsed: boolean;
  sourceCount: number;
  appCount: number;
  credentialCount: number;
  liveWorkers: number;
  onNavigate: (section: ConsoleSection) => void;
  onToggleCollapsed: () => void;
};

const navItems: Array<{ key: ConsoleSection; label: string; shortLabel: string; description: string }> = [
  { key: "deployments", label: "Deployments", shortLabel: "D", description: "Release control" },
  { key: "sources", label: "Sources", shortLabel: "S", description: "Git registrations" },
  { key: "releases", label: "Releases", shortLabel: "R", description: "Active contracts" },
  { key: "audit", label: "Audit", shortLabel: "A", description: "Deployment trail" },
  { key: "settings", label: "Settings", shortLabel: "G", description: "Workspace and actor" },
];

export function Sidebar({ active, collapsed, sourceCount, appCount, credentialCount, liveWorkers, onNavigate, onToggleCollapsed }: Props) {
  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"} aria-label="Windforce navigation">
      <div className="sidebarHeader">
        <div className="sidebarBrand">
          <div className="brandMark">WF</div>
          <div className="sidebarText">
            <strong>windforce-lite</strong>
            <span>Control plane</span>
          </div>
        </div>
        <button
          id="toggleSidebar"
          className="sidebarToggle"
          type="button"
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          onClick={onToggleCollapsed}
        >
          {collapsed ? ">>" : "<<"}
        </button>
      </div>

      <nav className="sideNav" aria-label="Main navigation">
        {navItems.map((item) => (
          <button
            key={item.key}
            className={`sideNavItem ${active === item.key ? "active" : ""}`}
            type="button"
            aria-label={item.label}
            title={collapsed ? `${item.label}: ${item.description}` : undefined}
            onClick={() => onNavigate(item.key)}
          >
            <span className="sideNavIcon" aria-hidden="true">{item.shortLabel}</span>
            <span className="sideNavText">
              <span>{item.label}</span>
              <small>{item.description}</small>
            </span>
          </button>
        ))}
      </nav>

      <div className="sidebarStatus" aria-label="Environment summary">
        <div>
          <span>Sources</span>
          <strong>{sourceCount}</strong>
        </div>
        <div>
          <span>Apps</span>
          <strong>{appCount}</strong>
        </div>
        <div>
          <span>Credentials</span>
          <strong>{credentialCount}</strong>
        </div>
        <div>
          <span>Workers</span>
          <strong>{liveWorkers}</strong>
        </div>
      </div>
    </aside>
  );
}
