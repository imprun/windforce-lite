import { Link, useRouter } from "../lib/router";

export const settingsNavItems = [
  { to: "/settings", label: "General", match: (path: string) => path === "/settings" },
  { to: "/settings/provisioning", label: "Provisioning", match: (path: string) => path === "/settings/provisioning" },
  { to: "/settings/webhooks", label: "Webhooks", match: (path: string) => path.startsWith("/settings/webhooks") },
  { to: "/settings/info", label: "Info", match: (path: string) => path === "/settings/info" },
];

export function SettingsNav() {
  const { path } = useRouter();
  return (
    <nav className="tabBar settingsNav" aria-label="Settings sections">
      {settingsNavItems.map((item) => (
        <Link key={item.to} className={item.match(path) ? "tab active" : "tab"} to={item.to}>
          {item.label}
        </Link>
      ))}
    </nav>
  );
}
