export default {
  order: 10,
  id: "workspaces",
  title: "Manage workspaces",
  description:
    "The workspace registry is the instance-admin surface for workspace identity, status, scoped access, and lifecycle operations.",
  screenshot: "docs/assets/ui/workspaces.png",
  guide: [
    "Select an active workspace from the sidebar.",
    "Open Workspaces from the primary navigation to review the instance registry.",
    "Create a workspace or open a workspace's dedicated administration page.",
    "Use an instance-admin token for workspace lifecycle operations; workspace tokens remain scoped to one workspace.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Workspaces");
    await page.waitForSelector("#workspaceRegistry tbody tr");
    await page.waitForText("#workspaceRegistry", "Operations");
    await capture(this.id);
  },
};
