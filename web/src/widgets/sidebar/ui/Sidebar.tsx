"use client";

type SectionKey = "deployments" | "sources" | "releases" | "audit";

type Props = {
  active: SectionKey;
  sourceCount: number;
  appCount: number;
  credentialCount: number;
  liveWorkers: number;
  onNavigate: (section: SectionKey) => void;
  onSettings: () => void;
};

const navItems: Array<{ key: SectionKey; label: string; description: string }> = [
  { key: "deployments", label: "Deployments", description: "Release console" },
  { key: "sources", label: "Sources", description: "Git registrations" },
  { key: "releases", label: "Releases", description: "Active contracts" },
  { key: "audit", label: "Audit", description: "Deployment trail" },
];

export function Sidebar({ active, sourceCount, appCount, credentialCount, liveWorkers, onNavigate, onSettings }: Props) {
  return (
    <aside className="sidebar" aria-label="Windforce navigation">
      <div className="sidebarBrand">
        <div className="brandMark">WF</div>
        <div>
          <strong>windforce-lite</strong>
          <span>Control plane</span>
        </div>
      </div>

      <nav className="sideNav" aria-label="Main navigation">
        {navItems.map((item) => (
          <button
            key={item.key}
            className={`sideNavItem ${active === item.key ? "active" : ""}`}
            type="button"
            aria-label={item.label}
            onClick={() => onNavigate(item.key)}
          >
            <span>{item.label}</span>
            <small>{item.description}</small>
          </button>
        ))}
        <button className="sideNavItem" type="button" aria-label="Settings" onClick={onSettings}>
          <span>Settings</span>
          <small>Workspace and actor</small>
        </button>
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
