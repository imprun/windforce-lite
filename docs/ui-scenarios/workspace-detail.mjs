export default {
  order: 11,
  id: "workspace-detail",
  title: "Administer a workspace",
  description:
    "Each workspace has a dedicated administration page that separates identity, access, audit, and lifecycle responsibilities.",
  screenshot: "docs/assets/ui/workspace-detail.png",
  guide: [
    "Open a workspace from the instance-level Workspace registry.",
    "Use Overview for its display name, Access for its scoped token, Audit for lifecycle history, and Lifecycle for archive controls.",
    "Use the sidebar workspace switcher only to change the active workspace for workspace-scoped operations.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Workspaces");
    await page.waitForSelector("#workspaceRegistry tbody tr");
    await page.clickText("Operations");
    await page.waitForText("main", "Workspace identity");
    await capture(this.id);
  },
};
