export default {
  order: 11,
  id: "workspace-detail",
  title: "Administer a workspace",
  description:
    "Each workspace has a dedicated instance-administration page without workspace-scoped application navigation.",
  screenshot: "docs/assets/ui/workspace-detail.png",
  guide: [
    "Open Manage workspaces from the workspace switcher, then choose a workspace from the registry.",
    "Use Overview for its display name, Access for its scoped token, Audit for lifecycle history, and Lifecycle for archive controls.",
    "Return to the workspace switcher when changing the active workspace for workspace-scoped operations.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click(".sidebarFooter .workspaceSwitcherTrigger");
    await page.click(".sidebarFooter .workspaceManageLink");
    await page.waitForSelector("#workspaceRegistry tbody tr");
    await page.clickText("Operations");
    await page.waitForText("main", "Workspace identity");
    await capture(this.id);
  },
};
