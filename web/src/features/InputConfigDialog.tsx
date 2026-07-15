import { Lock, Plus, Save, Trash2, Unlock } from "lucide-react";
import { useState } from "react";
import { Field, Modal } from "../components/ui";
import {
  errorMessage,
  type ActionView,
  type Client,
  type InputConfig,
  type InputConfigPayload,
} from "../lib/api";
import { actionDisplayName } from "../lib/action-label";
import { useApp } from "../lib/app-context";

export type InputConfigRow = {
  key: string;
  valueText: string;
  locked: boolean;
};

export function inputConfigRows(config?: InputConfig): InputConfigRow[] {
  if (!config) return [{ key: "", valueText: "", locked: false }];
  return Object.entries(config.config).map(([key, value]) => ({
    key,
    valueText: JSON.stringify(value),
    locked: config.locked_keys.includes(key),
  }));
}

export function inputConfigPayload(rows: InputConfigRow[], actionKey: string, clientID: string): InputConfigPayload {
  const config: Record<string, unknown> = {};
  const lockedKeys: string[] = [];
  for (const row of rows) {
    const key = row.key.trim();
    if (!key) continue;
    if (Object.prototype.hasOwnProperty.call(config, key)) throw new Error(`Duplicate key "${key}".`);
    try {
      config[key] = JSON.parse(row.valueText);
    } catch {
      throw new Error(`Value for "${key}" must be valid JSON.`);
    }
    if (row.locked) lockedKeys.push(key);
  }
  return { action_key: actionKey, client_id: clientID || undefined, config, locked_keys: lockedKeys };
}

export function InputConfigDialog({
  appKey,
  actions,
  clients,
  existing,
  fixedClientID,
  onClose,
  onSaved,
}: {
  appKey: string;
  actions: ActionView[];
  clients: Client[];
  existing?: InputConfig;
  fixedClientID?: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { api, notify } = useApp();
  const [actionKey, setActionKey] = useState(existing?.action_key || "");
  const [clientID, setClientID] = useState(fixedClientID ?? existing?.client_id ?? "");
  const [rows, setRows] = useState<InputConfigRow[]>(() => inputConfigRows(existing));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const identityLocked = Boolean(existing);

  function updateRow(index: number, patch: Partial<InputConfigRow>) {
    setRows((current) => current.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch } : row)));
  }

  async function save() {
    setError("");
    let payload: InputConfigPayload;
    try {
      payload = inputConfigPayload(rows, actionKey, clientID);
    } catch (cause) {
      setError(errorMessage(cause));
      return;
    }
    setBusy(true);
    try {
      await api.setInputConfig(appKey, payload);
      notify("ok", `Saved input settings for ${appKey}${actionKey ? ` / ${actionKey}` : ""}.`);
      onSaved();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!existing || !window.confirm("Delete this input-setting layer?")) return;
    setBusy(true);
    setError("");
    try {
      await api.deleteInputConfig(appKey, existing.action_key, existing.client_id || "");
      notify("ok", "Deleted input settings.");
      onSaved();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  const fixedClient = clients.find((client) => client.id === fixedClientID);
  return (
    <Modal
      title={existing ? "Edit Input Settings" : "Add Input Settings"}
      subtitle={`${appKey} · top-level JSON values applied before action execution`}
      onClose={onClose}
      wide
    >
      <div className="formGrid">
        <Field label="Client scope">
          {fixedClientID !== undefined ? (
            <input value={fixedClient?.name || fixedClientID} disabled />
          ) : (
            <select value={clientID} disabled={identityLocked} onChange={(event) => setClientID(event.target.value)}>
              <option value="">All clients (default)</option>
              {clients.map((client) => (
                <option key={client.id} value={client.id}>
                  {client.name}
                </option>
              ))}
            </select>
          )}
        </Field>
        <Field label="Action scope">
          <select value={actionKey} disabled={identityLocked} onChange={(event) => setActionKey(event.target.value)}>
            <option value="">All actions (app default)</option>
            {actions.map((action) => (
              <option key={action.action_key} value={action.action_key}>
                {actionDisplayName(action.display_name) || action.action_key} · {action.action_key}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <div className="inputConfigEditor">
        <div className="inputConfigHeader" aria-hidden="true">
          <span>Input key</span>
          <span>JSON value</span>
          <span>Lock</span>
          <span />
        </div>
        {rows.map((row, index) => (
          <div className="inputConfigRow" key={index}>
            <input
              className="mono"
              value={row.key}
              placeholder="region"
              onChange={(event) => updateRow(index, { key: event.target.value })}
              aria-label={`Input key ${index + 1}`}
            />
            <textarea
              className="mono"
              rows={1}
              value={row.valueText}
              placeholder={'"kr"'}
              onChange={(event) => updateRow(index, { valueText: event.target.value })}
              aria-label={`JSON value ${index + 1}`}
            />
            <button
              className={row.locked ? "button small primary iconButton" : "button small iconButton"}
              type="button"
              title={row.locked ? "Locked: request cannot override" : "Unlocked: request may override"}
              aria-label={row.locked ? "Unlock input key" : "Lock input key"}
              aria-pressed={row.locked}
              onClick={() => updateRow(index, { locked: !row.locked })}
            >
              {row.locked ? <Lock size={16} aria-hidden="true" /> : <Unlock size={16} aria-hidden="true" />}
            </button>
            <button
              className="button small iconButton"
              type="button"
              title="Remove key"
              aria-label="Remove input key"
              onClick={() => setRows((current) => current.filter((_, rowIndex) => rowIndex !== index))}
            >
              <Trash2 size={16} aria-hidden="true" />
            </button>
          </div>
        ))}
        <button
          className="button small inputConfigAdd"
          type="button"
          onClick={() => setRows((current) => [...current, { key: "", valueText: "", locked: false }])}
        >
          <Plus size={16} aria-hidden="true" />
          Add key
        </button>
      </div>

      {error ? <div className="inlineNotice error">{error}</div> : null}
      <footer className="dialogFooter">
        <span>
          {existing ? (
            <button className="button danger" type="button" disabled={busy} onClick={remove}>
              <Trash2 size={16} aria-hidden="true" />
              Delete layer
            </button>
          ) : null}
        </span>
        <div className="dialogFooterActions">
          <button className="button" type="button" disabled={busy} onClick={onClose}>
            Cancel
          </button>
          <button className="button primary" type="button" disabled={busy} onClick={save}>
            <Save size={16} aria-hidden="true" />
            {busy ? "Saving…" : "Save settings"}
          </button>
        </div>
      </footer>
    </Modal>
  );
}
