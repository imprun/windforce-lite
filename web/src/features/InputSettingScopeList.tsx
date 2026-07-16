import { Lock, Pencil, Unlock } from "lucide-react";
import { type ReactNode } from "react";
import { type InputConfig } from "../lib/api";
import { formatRelative, formatTime } from "../lib/format";

export function formatInputSettingValue(value: unknown): string {
  return JSON.stringify(value, null, 2) ?? String(value);
}

export type InputSettingScopeItem = {
  key: string;
  config: InputConfig;
  primaryLabel: string;
  primaryValue: ReactNode;
  primaryMeta: string;
  actionName: string;
  actionMeta: string;
  editLabel: string;
  editDisabled?: boolean;
  onEdit: () => void;
};

export function InputSettingScopeList({ id, items }: { id: string; items: InputSettingScopeItem[] }) {
  return (
    <div className="inputSettingsList" id={id}>
      {items.map((item) => (
        <section className="inputSettingScope" key={item.key}>
          <header className="inputSettingScopeHeader">
            <div className="inputSettingFact inputSettingPrimaryScope">
              <span className="inputSettingFactLabel">{item.primaryLabel}</span>
              <span className="inputSettingFactValue">{item.primaryValue}</span>
              <span className="inputSettingFactMeta">{item.primaryMeta}</span>
            </div>
            <div className="inputSettingFact inputSettingActionScope">
              <span className="inputSettingFactLabel">Action scope</span>
              <span className="inputSettingFactValue">{item.actionName}</span>
              <span className="inputSettingFactMeta mono">{item.actionMeta}</span>
            </div>
            <div className="inputSettingFact inputSettingChange" title={formatTime(item.config.updated_at)}>
              <span className="inputSettingFactLabel">Last change</span>
              <span className="inputSettingFactValue">{formatRelative(item.config.updated_at)}</span>
              <span className="inputSettingFactMeta">
                {formatTime(item.config.updated_at)} · {item.config.updated_by}
              </span>
            </div>
            <button
              className="button small iconButton inputSettingEdit"
              type="button"
              title="Edit input settings"
              aria-label={item.editLabel}
              disabled={item.editDisabled}
              onClick={item.onEdit}
            >
              <Pencil size={15} aria-hidden="true" />
            </button>
          </header>

          <div className="inputSettingValues" role="table" aria-label="Applied input values">
            <div className="inputSettingValuesHeader" role="row">
              <span role="columnheader">Input key</span>
              <span role="columnheader">Applied value</span>
              <span role="columnheader">Request policy</span>
            </div>
            {Object.entries(item.config.config).map(([key, value]) => {
              const locked = item.config.locked_keys.includes(key);
              return (
                <div className="inputSettingValueRow" role="row" key={key}>
                  <div className="inputSettingValueCell" role="cell">
                    <span className="inputSettingFieldLabel">Input key</span>
                    <code className="inputSettingKey">{key}</code>
                  </div>
                  <div className="inputSettingValueCell" role="cell">
                    <span className="inputSettingFieldLabel">Applied value</span>
                    <pre className="inputSettingValue">{formatInputSettingValue(value)}</pre>
                  </div>
                  <div className="inputSettingValueCell" role="cell">
                    <span className="inputSettingFieldLabel">Request policy</span>
                    <span className={locked ? "inputSettingPolicy locked" : "inputSettingPolicy"}>
                      {locked ? <Lock size={14} aria-hidden="true" /> : <Unlock size={14} aria-hidden="true" />}
                      {locked ? "Request cannot override" : "Request may override"}
                    </span>
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
