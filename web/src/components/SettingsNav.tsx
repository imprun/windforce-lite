import { Link, useRouter } from "../lib/router";

const items = [
  { to: "/settings", label: "General", match: (path: string) => path === "/settings" },
  { to: "/settings/webhooks", label: "Webhooks", match: (path: string) => path.startsWith("/settings/webhooks") },
];

export function SettingsNav() {
  const { path } = useRouter();
  return (
    <nav className="tabBar settingsNav" aria-label="Settings sections">
      {items.map((item) => (
        <Link key={item.to} className={item.match(path) ? "tab active" : "tab"} to={item.to}>
          {item.label}
        </Link>
      ))}
    </nav>
  );
}
