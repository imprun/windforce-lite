import {
  Activity,
  AppWindow,
  ArrowLeft,
  ChevronDown,
  CircleUserRound,
  ContactRound,
  Eraser,
  Menu,
  MonitorSmartphone,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  ScrollText,
  Settings,
  Sun,
  Wind,
  X,
} from "lucide-react";
import { Dialog as DialogPrimitive, DropdownMenu as DropdownMenuPrimitive } from "radix-ui";
import { type ReactNode, useEffect, useState } from "react";
import { useApp } from "../lib/app-context";
import { Link, useRouter } from "../lib/router";
import { cn } from "../shared/lib/cn";
import { useThemeStore } from "../shared/lib/theme";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";

export const primaryNavItems = [
  {
    to: "/",
    label: "Apps",
    icon: AppWindow,
    match: (path: string) => path === "/" || path.startsWith("/apps"),
  },
  {
    to: "/clients",
    label: "Client Registry",
    icon: ContactRound,
    match: (path: string) => path.startsWith("/clients"),
  },
  {
    to: "/monitoring",
    label: "Monitoring",
    icon: Activity,
    match: (path: string) => path.startsWith("/monitoring") || path.startsWith("/jobs"),
  },
  {
    to: "/audit",
    label: "Audit",
    icon: ScrollText,
    match: (path: string) => path.startsWith("/audit"),
  },
  {
    to: "/settings",
    label: "Settings",
    icon: Settings,
    match: (path: string) => path.startsWith("/settings") && path !== "/settings/workspaces",
  },
];

function loadCollapsed(): boolean {
  return globalThis.localStorage?.getItem("wf.sidebarCollapsed") === "true";
}

function ThemeToggle() {
  const preference = useThemeStore((state) => state.preference);
  const cycle = useThemeStore((state) => state.cycle);
  const Icon = preference === "light" ? Sun : preference === "dark" ? Moon : MonitorSmartphone;
  const label = preference === "light" ? "light" : preference === "dark" ? "dark" : "system";
  return (
    <button
      type="button"
      className="icon-control"
      onClick={cycle}
      title={`Theme: ${label}`}
      aria-label={`Change theme (current: ${label})`}
    >
      <Icon size={16} />
    </button>
  );
}

export function UserMenu() {
  const { settings, clearLocalCredentials, notify } = useApp();
  const { navigate } = useRouter();
  const hasApiToken = Boolean(settings.token);
  const hasBrowserIdentity = Boolean(settings.actor || settings.token);

  function handleClearLocalCredentials() {
    clearLocalCredentials();
    navigate("/settings");
    notify("info", "Local API token and audit actor cleared from this browser.");
  }

  const itemClass =
    "flex cursor-pointer select-none items-center gap-2 rounded px-2 py-2 text-sm outline-none data-[disabled]:cursor-not-allowed data-[disabled]:opacity-45 data-[highlighted]:bg-muted";

  return (
    <DropdownMenuPrimitive.Root modal={false}>
      <DropdownMenuPrimitive.Trigger asChild>
        <button
          type="button"
          className="flex min-w-0 items-center gap-2 rounded-md px-2 py-1 text-left hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
          aria-label={`User menu for ${settings.actor || "system"}`}
        >
          <CircleUserRound className="shrink-0 text-muted-foreground" size={18} />
          <span className="hidden min-w-0 sm:block">
            <span className="block truncate text-sm font-medium leading-tight">
              {settings.actor || "system"}
            </span>
            <span className="block text-xs leading-tight text-muted-foreground">Audit actor</span>
          </span>
          <ChevronDown className="shrink-0 text-muted-foreground" size={14} />
        </button>
      </DropdownMenuPrimitive.Trigger>
      <DropdownMenuPrimitive.Portal>
        <DropdownMenuPrimitive.Content
          align="end"
          sideOffset={8}
          className="z-[100] min-w-56 rounded-md border border-border bg-surface p-1 text-foreground shadow-lg"
        >
          <DropdownMenuPrimitive.Label className="px-2 py-2">
            <span className="block text-sm font-medium">{settings.actor || "system"}</span>
            <span className="block text-xs text-muted-foreground">
              {hasApiToken ? "API token configured" : "API token not configured"}
            </span>
          </DropdownMenuPrimitive.Label>
          <DropdownMenuPrimitive.Separator className="my-1 h-px bg-border" />
          <DropdownMenuPrimitive.Item className={itemClass} onSelect={() => navigate("/settings")}>
            <Settings size={16} />
            Browser API settings
          </DropdownMenuPrimitive.Item>
          <DropdownMenuPrimitive.Item
            className={itemClass}
            disabled={!hasBrowserIdentity}
            onSelect={handleClearLocalCredentials}
          >
            <Eraser size={16} />
            {hasBrowserIdentity ? "Clear local credentials" : "No local credentials"}
          </DropdownMenuPrimitive.Item>
        </DropdownMenuPrimitive.Content>
      </DropdownMenuPrimitive.Portal>
    </DropdownMenuPrimitive.Root>
  );
}

function MobileNavigation({ path }: { path: string }) {
  const [open, setOpen] = useState(false);

  return (
    <DialogPrimitive.Root open={open} onOpenChange={setOpen}>
      <DialogPrimitive.Trigger asChild>
        <button className="icon-control md:hidden" type="button" aria-label="Open navigation menu">
          <Menu size={18} aria-hidden="true" />
        </button>
      </DialogPrimitive.Trigger>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-[80] bg-[var(--overlay)] md:hidden" />
        <DialogPrimitive.Content
          className="fixed inset-y-0 left-0 z-[81] flex w-[min(20rem,calc(100vw-3rem))] flex-col border-r border-border bg-surface text-foreground shadow-md outline-none md:hidden"
          aria-describedby={undefined}
        >
          <header className="flex h-14 shrink-0 items-center justify-between border-b border-border px-3">
            <DialogPrimitive.Title className="flex items-center gap-2 text-sm font-semibold">
              <span className="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground">
                <Wind size={16} strokeWidth={2.2} aria-hidden="true" />
              </span>
              windforce-core
            </DialogPrimitive.Title>
            <DialogPrimitive.Close className="icon-control" aria-label="Close navigation menu">
              <X size={17} aria-hidden="true" />
            </DialogPrimitive.Close>
          </header>
          <nav className="flex flex-1 flex-col gap-1 overflow-y-auto px-3 py-4" aria-label="Mobile">
            {primaryNavItems.map((item) => {
              const Icon = item.icon;
              const active = item.match(path);
              return (
                <Link
                  key={item.to}
                  to={item.to}
                  className={cn("navItem", active && "active")}
                  onClick={() => setOpen(false)}
                >
                  <Icon size={17} strokeWidth={1.9} aria-hidden="true" />
                  <span>{item.label}</span>
                </Link>
              );
            })}
          </nav>
          <div className="border-t border-border p-3">
            <WorkspaceSwitcher />
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

export function Layout({
  title,
  subtitle,
  actions,
  children,
  scope = "workspace",
  titleLeading,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
  scope?: "workspace" | "instance";
  titleLeading?: ReactNode;
}) {
  const { path } = useRouter();
  const { toasts, dismissToast } = useApp();
  const [collapsed, setCollapsed] = useState(loadCollapsed);

  useEffect(() => {
    globalThis.localStorage?.setItem("wf.sidebarCollapsed", String(collapsed));
  }, [collapsed]);

  if (scope === "instance") {
    return (
      <div className="min-h-screen bg-background text-foreground">
        <header className="flex h-14 items-center justify-between border-b border-border bg-surface px-4 sm:px-6">
          <div className="flex items-center gap-3">
            <Link
              className="flex items-center gap-2 text-sm font-semibold text-foreground no-underline"
              to="/"
            >
              <span className="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground">
                <Wind size={16} strokeWidth={2.2} />
              </span>
              windforce-core
            </Link>
            <span className="h-5 w-px bg-border" aria-hidden="true" />
            <span className="text-xs font-medium text-muted-foreground">
              Instance administration
            </span>
          </div>
          <div className="flex items-center gap-2">
            <ThemeToggle />
            <UserMenu />
            <Link className="button small" to="/">
              <ArrowLeft size={15} /> Back to workspace
            </Link>
          </div>
        </header>
        <main className="mx-auto w-full max-w-[var(--content-max-width)] px-4 py-6 sm:px-6">
          <PageHeading
            title={title}
            subtitle={subtitle}
            actions={actions}
            titleLeading={titleLeading}
          />
          <div className="mt-6 flex flex-col gap-4">{children}</div>
        </main>
        <ToastStack toasts={toasts} dismissToast={dismissToast} />
      </div>
    );
  }

  return (
    <div className="flex min-h-screen bg-background text-foreground">
      <aside
        className={cn(
          "appSidebar hidden h-screen shrink-0 flex-col border-r border-border bg-surface transition-[width] duration-150 md:sticky md:top-0 md:flex",
          collapsed && "sidebarCollapsed",
          collapsed ? "w-16" : "w-60",
        )}
      >
        <div className="flex h-14 items-center gap-2 border-b border-border px-3">
          <Link
            className={cn(
              "brand flex min-w-0 flex-1 items-center gap-2 text-sm font-semibold text-foreground no-underline",
              collapsed && "justify-center",
            )}
            to="/"
            title="windforce-core"
          >
            <span className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
              <Wind size={16} strokeWidth={2.2} />
            </span>
            {!collapsed ? <span className="truncate">windforce-core</span> : null}
          </Link>
          {!collapsed ? (
            <button
              className="icon-control"
              id="sidebarToggle"
              type="button"
              aria-label="Collapse sidebar"
              onClick={() => setCollapsed(true)}
            >
              <PanelLeftClose size={17} />
            </button>
          ) : null}
        </div>
        <nav className="flex flex-1 flex-col gap-1 overflow-y-auto px-3 py-4" aria-label="Primary">
          {primaryNavItems.map((item) => {
            const Icon = item.icon;
            const active = item.match(path);
            return (
              <Link
                key={item.to}
                to={item.to}
                className={cn("navItem", active && "active", collapsed && "justify-center px-0")}
                title={item.label}
              >
                <Icon size={17} strokeWidth={1.9} aria-hidden="true" />
                {!collapsed ? <span>{item.label}</span> : null}
              </Link>
            );
          })}
        </nav>
        <div className="sidebarFooter flex flex-col gap-2 border-t border-border p-3">
          {collapsed ? (
            <button
              className="icon-control mx-auto"
              id="sidebarToggle"
              type="button"
              aria-label="Expand sidebar"
              onClick={() => setCollapsed(false)}
            >
              <PanelLeftOpen size={17} />
            </button>
          ) : (
            <WorkspaceSwitcher />
          )}
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-border bg-surface px-4 sm:px-6">
          <div className="flex min-w-0 items-center gap-2 md:hidden">
            <MobileNavigation path={path} />
            <div className="mobileWorkspaceContext min-w-0">
              <WorkspaceSwitcher />
            </div>
          </div>
          <div className="ml-auto flex items-center gap-2">
            <ThemeToggle />
            <span className="hidden h-6 w-px bg-border sm:block" aria-hidden="true" />
            <UserMenu />
          </div>
        </header>
        <main className="min-w-0 flex-1 overflow-y-auto">
          <div className="mx-auto w-full max-w-[var(--content-max-width)] px-4 py-6 sm:px-6">
            <PageHeading
              title={title}
              subtitle={subtitle}
              actions={actions}
              titleLeading={titleLeading}
            />
            <div className="mt-6 flex flex-col gap-4">{children}</div>
          </div>
        </main>
      </div>
      <ToastStack toasts={toasts} dismissToast={dismissToast} />
    </div>
  );
}

function PageHeading({
  title,
  subtitle,
  actions,
  titleLeading,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  titleLeading?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="flex min-w-0 items-start gap-3">
        {titleLeading}
        <div className="min-w-0">
          <h1 className="text-xl font-semibold text-balance">{title}</h1>
          {subtitle ? (
            <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{subtitle}</p>
          ) : null}
        </div>
      </div>
      {actions ? (
        <div className="topbarActions flex flex-wrap items-center gap-2">{actions}</div>
      ) : null}
    </div>
  );
}

function ToastStack({
  toasts,
  dismissToast,
}: {
  toasts: Array<{ id: number; text: string; tone: string }>;
  dismissToast: (id: number) => void;
}) {
  return (
    <div className="toastStack" aria-live="polite">
      {toasts.map((toast) => (
        <div key={toast.id} className={`toast toast-${toast.tone}`} id="toast">
          <span>{toast.text}</span>
          <button
            type="button"
            className="icon-control"
            aria-label="Dismiss"
            onClick={() => dismissToast(toast.id)}
          >
            <X size={15} />
          </button>
        </div>
      ))}
    </div>
  );
}
