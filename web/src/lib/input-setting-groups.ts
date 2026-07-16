import { type InputConfig } from "./api";

export type InputSettingGroup = {
  key: string;
  configs: InputConfig[];
  actionKeys: string[];
  keyNames: string[];
  valueCount: number;
  lockedCount: number;
  updatedAt: string;
  updatedBy: string;
};

export function groupInputSettings(
  configs: InputConfig[],
  groupKey: (config: InputConfig) => string,
): InputSettingGroup[] {
  const groups = new Map<string, InputConfig[]>();
  for (const config of configs) {
    const key = groupKey(config);
    groups.set(key, [...(groups.get(key) || []), config]);
  }

  return [...groups.entries()].map(([key, groupedConfigs]) => {
    const latest = groupedConfigs.reduce((current, config) =>
      Date.parse(config.updated_at) > Date.parse(current.updated_at) ? config : current,
    );
    return {
      key,
      configs: groupedConfigs,
      actionKeys: [...new Set(groupedConfigs.map((config) => config.action_key))],
      keyNames: [...new Set(groupedConfigs.flatMap((config) => Object.keys(config.config)))].sort(),
      valueCount: groupedConfigs.reduce((total, config) => total + Object.keys(config.config).length, 0),
      lockedCount: groupedConfigs.reduce((total, config) => total + config.locked_keys.length, 0),
      updatedAt: latest.updated_at,
      updatedBy: latest.updated_by,
    };
  });
}

export function inputSettingGroupMatches(group: InputSettingGroup, query: string, aliases: string[] = []): boolean {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return true;
  return [group.key, ...group.actionKeys, ...group.keyNames, ...aliases]
    .join(" ")
    .toLowerCase()
    .includes(normalized);
}

export function paginate<T>(items: T[], requestedPage: number, pageSize: number) {
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const page = Math.min(Math.max(1, requestedPage), totalPages);
  const start = (page - 1) * pageSize;
  return { items: items.slice(start, start + pageSize), page, totalPages, start };
}
