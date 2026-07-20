import { Boxes, Check, ChevronRight, ChevronsUpDown, Search } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useApp, useAsync } from "../lib/app-context";
import { Link, useRouter } from "../lib/router";
import { filterWorkspaces, visibleWorkspaces, WORKSPACE_REGISTRY_CHANGED } from "../lib/workspaces";

export function WorkspaceSwitcher() {
  const { api, settings, updateSettings } = useApp();
  const { navigate } = useRouter();
  const state = useAsync(() => api.workspaces(), [api]);
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const rootRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const firstOptionRef = useRef<HTMLButtonElement>(null);
  const popoverID = useId();
  const workspaces = useMemo(
    () => visibleWorkspaces(state.data?.items || [], settings.workspace),
    [settings.workspace, state.data],
  );
  const filtered = useMemo(() => filterWorkspaces(workspaces, query), [query, workspaces]);
  const current = state.data?.items.find((workspace) => workspace.id === settings.workspace);

  useEffect(() => {
    window.addEventListener(WORKSPACE_REGISTRY_CHANGED, state.reload);
    return () => window.removeEventListener(WORKSPACE_REGISTRY_CHANGED, state.reload);
  }, [state.reload]);

  useEffect(() => {
    if (!open) return;
    const closeOnPointerDown = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("pointerdown", closeOnPointerDown);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnPointerDown);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    window.requestAnimationFrame(() => {
      if (workspaces.length > 5) searchRef.current?.focus();
      else firstOptionRef.current?.focus();
    });
  }, [open, workspaces.length]);

  function toggle() {
    setQuery("");
    setOpen((value) => !value);
  }

  function switchWorkspace(workspaceID: string) {
    setOpen(false);
    if (workspaceID === settings.workspace) return;
    updateSettings({ ...settings, workspace: workspaceID });
    navigate("/");
  }

  return (
    <div className="workspaceSwitcher" ref={rootRef}>
      <button
        className="workspaceSwitcherTrigger"
        type="button"
        ref={triggerRef}
        aria-label={`Current workspace: ${current?.name || settings.workspace}`}
        aria-expanded={open}
        aria-controls={popoverID}
        aria-haspopup="dialog"
        title={`Workspace: ${current?.name || settings.workspace}`}
        onClick={toggle}
      >
        <span className="workspaceSwitcherIcon" aria-hidden="true"><Boxes size={17} /></span>
        <span className="workspaceSwitcherText">
          <span className="workspaceSwitcherName">{current?.name || settings.workspace}</span>
          <span className="workspaceSwitcherID">{settings.workspace}</span>
        </span>
        <ChevronsUpDown className="workspaceSwitcherChevron" size={15} aria-hidden="true" />
      </button>

      {open ? (
        <div className="workspacePopover" id={popoverID} role="dialog" aria-label="Switch workspace">
          <div className="workspacePopoverHeader">
            <strong>Switch workspace</strong>
            <span>{workspaces.length} available</span>
          </div>
          {workspaces.length > 5 ? (
            <label className="workspaceSearch">
              <Search size={15} aria-hidden="true" />
              <input
                ref={searchRef}
                type="search"
                value={query}
                placeholder="Find a workspace"
                aria-label="Find a workspace"
                onChange={(event) => setQuery(event.target.value)}
              />
            </label>
          ) : null}
          <div className="workspaceOptionList" role="listbox" aria-label="Available workspaces">
            {state.loading && !state.data ? <span className="workspacePopoverState">Loading workspaces…</span> : null}
            {state.error ? <span className="workspacePopoverState errorText">Could not load workspace list.</span> : null}
            {!state.loading && !state.error && filtered.length === 0 ? <span className="workspacePopoverState">No matching workspaces.</span> : null}
            {filtered.map((workspace, index) => {
              const selected = workspace.id === settings.workspace;
              return (
                <button
                  key={workspace.id}
                  className={selected ? "workspaceOption selected" : "workspaceOption"}
                  type="button"
                  role="option"
                  aria-selected={selected}
                  ref={index === 0 ? firstOptionRef : undefined}
                  onClick={() => switchWorkspace(workspace.id)}
                >
                  <span className="workspaceOptionMark" aria-hidden="true">{selected ? <Check size={16} /> : null}</span>
                  <span className="workspaceOptionIdentity">
                    <strong>{workspace.name}</strong>
                    <span className="mono">{workspace.id}</span>
                  </span>
                  {workspace.status === "archived" ? <span className="badge badge-neutral">Archived</span> : null}
                </button>
              );
            })}
          </div>
          <Link className="workspaceManageLink" to="/workspaces" onClick={() => setOpen(false)}>
            <span>
              <strong>Manage workspaces</strong>
              <small>Create, access, and lifecycle</small>
            </span>
            <ChevronRight size={16} aria-hidden="true" />
          </Link>
        </div>
      ) : null}
    </div>
  );
}
