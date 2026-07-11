"use client";

import type { ApiSettings } from "@/shared/api/types";

type Props = {
  title: string;
  subtitle: string;
  settings: ApiSettings;
  busy: boolean;
  onRefresh: () => void;
  onRegister: () => void;
  onSettings: () => void;
};

export function Topbar({ title, subtitle, settings, busy, onRefresh, onRegister, onSettings }: Props) {
  return (
    <header className="topbar">
      <div className="pageTitle">
        <h1>{title}</h1>
        <p>{subtitle}</p>
      </div>
      <div className="topbarMeta" aria-label="Control plane context">
        <span className="contextPill">Workspace <strong>{settings.workspace || "default"}</strong></span>
        <span className={settings.actor ? "contextPill ok" : "contextPill warn"}>Actor <strong>{settings.actor || "not set"}</strong></span>
        <span className={settings.token ? "contextPill ok" : "contextPill"}>API token <strong>{settings.token ? "set" : "optional"}</strong></span>
      </div>
      <div className="topbarActions">
        <button id="openRegisterSource" className="button primary" type="button" aria-label="Register source from command bar" onClick={onRegister}>
          Register Source
        </button>
        <button className="button" type="button" onClick={onRefresh} disabled={busy}>
          {busy ? "Refreshing" : "Refresh"}
        </button>
        <button id="openSettings" className="button" type="button" onClick={onSettings}>
          Settings
        </button>
      </div>
    </header>
  );
}
