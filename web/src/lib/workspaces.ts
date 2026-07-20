import type { Workspace } from "./api";

export const WORKSPACE_REGISTRY_CHANGED = "windforce:workspace-registry-changed";

export function visibleWorkspaces(workspaces: Workspace[], currentWorkspace: string): Workspace[] {
  return workspaces.filter((workspace) => workspace.status === "active" || workspace.id === currentWorkspace);
}

export function filterWorkspaces(workspaces: Workspace[], query: string): Workspace[] {
  const normalized = query.trim().toLocaleLowerCase();
  if (!normalized) return workspaces;
  return workspaces.filter((workspace) =>
    workspace.name.toLocaleLowerCase().includes(normalized) || workspace.id.toLocaleLowerCase().includes(normalized),
  );
}

export function notifyWorkspaceRegistryChanged(): void {
  window.dispatchEvent(new Event(WORKSPACE_REGISTRY_CHANGED));
}
