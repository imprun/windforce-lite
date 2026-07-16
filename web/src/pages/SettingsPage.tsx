import { useEffect, useState } from "react";
import { Layout } from "../components/Layout";
import { SettingsNav } from "../components/SettingsNav";
import { DefinitionList, Field, Panel } from "../components/ui";
import { useApp } from "../lib/app-context";

export function SettingsPage() {
  const { settings, updateSettings, api, notify } = useApp();
  const [workspace, setWorkspace] = useState(settings.workspace);
  const [token, setToken] = useState(settings.token);
  const [actor, setActor] = useState(settings.actor);
  const [health, setHealth] = useState<string>("checking…");

  useEffect(() => {
    setWorkspace(settings.workspace);
    setToken(settings.token);
    setActor(settings.actor);
  }, [settings]);

  useEffect(() => {
    let canceled = false;
    fetch("/readyz")
      .then((response) => response.json())
      .then((payload: { ready?: boolean }) => {
        if (!canceled) setHealth(payload.ready ? "control plane ready" : "control plane not ready");
      })
      .catch(() => {
        if (!canceled) setHealth("control plane unreachable");
      });
    return () => {
      canceled = true;
    };
  }, [api]);

  const dirty = workspace !== settings.workspace || token !== settings.token || actor !== settings.actor;

  function handleSave() {
    updateSettings({
      workspace: workspace.trim() || "default",
      token: token.trim(),
      actor: actor.trim(),
    });
    notify("ok", "Settings saved.");
  }

  return (
    <Layout
      title="Settings"
      subtitle="Control-plane context used by every Web UI request. Stored in this browser only."
      actions={
        <button className="button primary" type="button" id="saveSettings" disabled={!dirty} onClick={handleSave}>
          Save settings
        </button>
      }
    >
      <SettingsNav />
      <Panel title="Control plane" subtitle="Workspace and API token for control-plane requests.">
        <div className="formGrid">
          <Field label="Workspace">
            <input id="settingsWorkspace" value={workspace} onChange={(event) => setWorkspace(event.target.value)} />
          </Field>
          <Field label="API token" hint="Sent as Authorization: Bearer. Leave empty when the control plane runs without --admin-token-env.">
            <input
              id="settingsToken"
              type="password"
              value={token}
              onChange={(event) => setToken(event.target.value)}
              autoComplete="off"
            />
          </Field>
        </div>
        <DefinitionList items={[["Status", health]]} />
      </Panel>

      <Panel
        title="Audit actor"
        subtitle="Recorded as the subject of releases, cancels, and other state changes. Not an authentication credential."
      >
        <div className="formGrid">
          <Field label="Actor" hint="With real authentication the actor comes from the request principal; local development defaults to local-dev.">
            <input id="settingsActor" value={actor} onChange={(event) => setActor(event.target.value)} />
          </Field>
        </div>
      </Panel>
    </Layout>
  );
}
