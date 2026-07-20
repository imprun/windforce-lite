import { describe, expect, test } from "bun:test";

const registrySource = await Bun.file(new URL("./WorkspacesPage.tsx", import.meta.url)).text();
const detailSource = await Bun.file(new URL("./WorkspaceDetailPage.tsx", import.meta.url)).text();
const adminSource = await Bun.file(new URL("../features/WorkspaceAdmin.tsx", import.meta.url)).text();

describe("workspace administration shell", () => {
  test("uses the instance shell for the registry", () => {
    expect(registrySource).toContain('scope="instance"');
  });

  test("uses the instance shell for detail, loading, and error states", () => {
    expect(detailSource.match(/scope="instance"/g)?.length).toBe(3);
  });

  test("aligns the workspace registry back control with the detail title", () => {
    expect(detailSource).toContain('className="button iconButton topbarTitleBack"');
    expect(detailSource.match(/titleLeading={backToWorkspaces}/g)?.length).toBe(3);
    expect(detailSource).not.toContain('actions={<Link className="button" to="/workspaces">');
  });

  test("offers workspace switching from registry and detail administration", () => {
    expect(registrySource).toContain("<WorkspaceActivation workspace={workspace} compact />");
    expect(detailSource).toContain("actions={<WorkspaceActivation workspace={workspace} />}");
    expect(adminSource).toContain("updateSettings({ ...settings, workspace: workspace.id })");
    expect(adminSource).toContain('navigate("/")');
  });

  test("uses a constrained identity grid", () => {
    expect(detailSource).toContain('className="workspaceIdentityFacts"');
  });
});
