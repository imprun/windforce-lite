import { BookOpen, Lock, Plus, Save, Trash2, Unlock } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
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
import {
  formatInputSettingExample,
  inputSettingDefinitions,
  validateInputSettingValue,
  type InputSettingDefinition,
} from "../lib/input-setting-schema";
import { formatSchemaValue } from "../lib/schema-document";

export type InputConfigRow = {
  key: string;
  valueText: string;
  locked: boolean;
  custom?: boolean;
};

export function inputConfigRows(config?: InputConfig): InputConfigRow[] {
  if (!config) return [{ key: "", valueText: "", locked: false }];
  return Object.entries(config.config).map(([key, value]) => ({
    key,
    valueText: formatInputSettingExample(value),
    locked: config.locked_keys.includes(key),
  }));
}

export function inputConfigPayload(
  rows: InputConfigRow[],
  actionKey: string,
  clientID: string,
  definitions: InputSettingDefinition[] = [],
): InputConfigPayload {
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
    const definition = definitions.find((candidate) => candidate.key === key);
    if (definition) {
      const validationError = validateInputSettingValue(definition, config[key]);
      if (validationError) throw new Error(validationError);
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
  const [schemaState, setSchemaState] = useState<{
    input: unknown;
    operator: unknown;
    loading: boolean;
    error: string;
  }>({ input: {}, operator: {}, loading: false, error: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const identityLocked = Boolean(existing);
  const definitions = useMemo(
    () => inputSettingDefinitions(schemaState.input, schemaState.operator),
    [schemaState.input, schemaState.operator],
  );
  const operatorDefinitions = definitions.filter((definition) => definition.source === "operator");
  const requestDefinitions = definitions.filter((definition) => definition.source === "request");

  useEffect(() => {
    let active = true;
    if (!actionKey) {
      setSchemaState({ input: {}, operator: {}, loading: false, error: "" });
      return () => {
        active = false;
      };
    }
    setSchemaState((current) => ({ ...current, loading: true, error: "" }));
    api
      .actionSchemas(appKey, actionKey)
      .then((schemas) => {
        if (!active) return;
        setSchemaState({
          input: schemas.input_schema,
          operator: schemas.operator_settings_schema,
          loading: false,
          error: "",
        });
      })
      .catch((cause) => {
        if (!active) return;
        setSchemaState({ input: {}, operator: {}, loading: false, error: errorMessage(cause) });
      });
    return () => {
      active = false;
    };
  }, [actionKey, api, appKey]);

  function updateRow(index: number, patch: Partial<InputConfigRow>) {
    setRows((current) => current.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch } : row)));
  }

  async function save() {
    setError("");
    let payload: InputConfigPayload;
    try {
      payload = inputConfigPayload(rows, actionKey, clientID, definitions);
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
  const fixedClientLabel = fixedClientID === "" ? "All clients (default)" : fixedClient?.name || fixedClientID;
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
            <input value={fixedClientLabel} disabled />
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

      {!actionKey ? (
        <div className="inlineNotice info">
          Choose an action to use its documented request fields and operator settings. App-default keys can still be
          entered manually.
        </div>
      ) : schemaState.loading ? (
        <div className="inlineNotice info">Loading documented settings for this release…</div>
      ) : schemaState.error ? (
        <div className="inlineNotice warning">
          Documented settings could not be loaded. Custom keys remain available. {schemaState.error}
        </div>
      ) : definitions.length === 0 ? (
        <div className="inlineNotice info">
          This action does not publish documented settings. Use a custom key only when the app implementation expects it.
        </div>
      ) : (
        <div className="inputConfigCatalogSummary">
          <BookOpen size={16} aria-hidden="true" />
          <span>
            This release documents <strong>{operatorDefinitions.length}</strong> operator setting(s) and{" "}
            <strong>{requestDefinitions.length}</strong> request field(s) that can be pre-applied.
          </span>
        </div>
      )}

      <div className="inputConfigEditor">
        {rows.map((row, index) => {
          const definition = definitions.find((candidate) => candidate.key === row.key);
          const custom = row.custom || Boolean(row.key && !definition);
          return (
            <div className="inputConfigRow" key={index}>
              <div className="inputConfigKeyField">
                <label htmlFor={`input-setting-key-${index}`}>Setting key</label>
                {actionKey && !schemaState.loading && definitions.length > 0 ? (
                  <select
                    id={`input-setting-key-${index}`}
                    value={custom ? "__custom__" : row.key}
                    onChange={(event) => {
                      const key = event.target.value;
                      if (key === "__custom__") {
                        updateRow(index, { key: definition ? "" : row.key, custom: true, valueText: definition ? "" : row.valueText });
                        return;
                      }
                      const selected = definitions.find((candidate) => candidate.key === key);
                      updateRow(index, {
                        key,
                        custom: false,
                        valueText: selected ? formatInputSettingExample(selected.example) : "",
                      });
                    }}
                    aria-label={`Setting key ${index + 1}`}
                  >
                    <option value="">Select a documented key</option>
                    {operatorDefinitions.length > 0 ? (
                      <optgroup label="Operator settings">
                        {operatorDefinitions.map((candidate) => (
                          <option value={candidate.key} key={candidate.key}>
                            {candidate.title ? `${candidate.title} · ` : ""}{candidate.key}
                          </option>
                        ))}
                      </optgroup>
                    ) : null}
                    {requestDefinitions.length > 0 ? (
                      <optgroup label="Request fields">
                        {requestDefinitions.map((candidate) => (
                          <option value={candidate.key} key={candidate.key}>
                            {candidate.title ? `${candidate.title} · ` : ""}{candidate.key}
                          </option>
                        ))}
                      </optgroup>
                    ) : null}
                    <option value="__custom__">Custom key…</option>
                  </select>
                ) : (
                  <input
                    id={`input-setting-key-${index}`}
                    className="mono"
                    value={row.key}
                    placeholder="SETTING_KEY"
                    onChange={(event) => updateRow(index, { key: event.target.value, custom: true })}
                    aria-label={`Setting key ${index + 1}`}
                  />
                )}
                {custom && actionKey && definitions.length > 0 ? (
                  <input
                    className="mono inputConfigCustomKey"
                    value={row.key}
                    placeholder="CUSTOM_SETTING_KEY"
                    onChange={(event) => updateRow(index, { key: event.target.value, custom: true })}
                    aria-label={`Custom setting key ${index + 1}`}
                  />
                ) : null}
              </div>
              <div className="inputConfigValueField">
                <label htmlFor={`input-setting-value-${index}`}>Applied value</label>
                <InputSettingValueEditor
                  id={`input-setting-value-${index}`}
                  row={row}
                  definition={definition}
                  onChange={(valueText) => updateRow(index, { valueText })}
                />
              </div>
              <div className="inputConfigRowActions">
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
              {definition ? (
                <InputSettingGuide
                  definition={definition}
                  onUseExample={() => updateRow(index, { valueText: formatInputSettingExample(definition.example) })}
                />
              ) : (
                <p className="inputConfigCustomHelp">
                  Custom keys have no release-provided validation or guidance. Confirm the key and JSON shape with the app owner.
                </p>
              )}
            </div>
          );
        })}
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

function InputSettingValueEditor({
  id,
  row,
  definition,
  onChange,
}: {
  id: string;
  row: InputConfigRow;
  definition?: InputSettingDefinition;
  onChange: (valueText: string) => void;
}) {
  if (definition?.constValue !== undefined) {
    return <input id={id} className="mono" value={row.valueText} disabled />;
  }
  if (definition?.enumValues?.length) {
    return (
      <select id={id} className="mono" value={normalizedEnumValue(row.valueText, definition.enumValues)} onChange={(event) => onChange(event.target.value)}>
        <option value="">Select an allowed value</option>
        {definition.enumValues.map((value) => {
          const encoded = formatInputSettingExample(value);
          return (
            <option value={encoded} key={encoded}>
              {formatSchemaValue(value)}
            </option>
          );
        })}
      </select>
    );
  }
  if (definition?.type === "boolean") {
    return (
      <label className="inputConfigBoolean" htmlFor={id}>
        <input id={id} type="checkbox" checked={row.valueText === "true"} onChange={(event) => onChange(String(event.target.checked))} />
        <span>{row.valueText === "true" ? "Enabled" : "Disabled"}</span>
      </label>
    );
  }
  if (definition?.type === "number" || definition?.type === "integer") {
    return <input id={id} type="number" value={row.valueText} onChange={(event) => onChange(event.target.value)} />;
  }
  return (
    <textarea
      id={id}
      className="mono"
      rows={definition?.type === "object" || definition?.type === "array" ? 5 : 2}
      value={row.valueText}
      placeholder={definition ? formatInputSettingExample(definition.example) : '{"key":"value"}'}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

function InputSettingGuide({ definition, onUseExample }: { definition: InputSettingDefinition; onUseExample: () => void }) {
  return (
    <aside className="inputConfigGuide">
      <div className="inputConfigGuideHeading">
        <div>
          <span className={definition.source === "operator" ? "badge info" : "badge neutral"}>
            {definition.source === "operator" ? "Operator setting" : "Request field"}
          </span>
          <span className="badge neutral mono">{definition.type}</span>
        </div>
        <button className="button small" type="button" onClick={onUseExample}>
          Use example
        </button>
      </div>
      <strong>{definition.title || definition.key}</strong>
      <p>{definition.description || "No description was provided by this release."}</p>
      {definition.fields.length > 0 ? (
        <div className="inputConfigFieldGuide" role="table" aria-label={`${definition.key} fields`}>
          {definition.fields.map((field) => (
            <div className="inputConfigFieldGuideRow" role="row" key={field.name}>
              <code role="cell">{field.name}</code>
              <span role="cell">
                {field.title || field.description || "No description"}
                <small>
                  {field.type}
                  {field.required ? " · required" : " · optional"}
                  {field.enumValues?.length ? ` · ${field.enumValues.map(formatSchemaValue).join(" | ")}` : ""}
                </small>
              </span>
            </div>
          ))}
        </div>
      ) : null}
      <div className="inputConfigExample">
        <span>Example</span>
        <pre>{formatInputSettingExample(definition.example)}</pre>
      </div>
    </aside>
  );
}

function normalizedEnumValue(valueText: string, values: unknown[]): string {
  try {
    const parsed = JSON.parse(valueText);
    const match = values.find((value) => JSON.stringify(value) === JSON.stringify(parsed));
    return match === undefined ? "" : formatInputSettingExample(match);
  } catch {
    return "";
  }
}
