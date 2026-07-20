export default {
  order: 10,
  id: "workspaces",
  title: "Manage workspaces",
  description:
    "The workspace registry uses an instance-administration shell without the active workspace's application navigation.",
  screenshot: "docs/assets/ui/workspaces.png",
  guide: [
    "Open the workspace switcher at the bottom of the sidebar.",
    "Choose Manage workspaces to review the instance registry.",
    "Use Switch to select a workspace for application and monitoring operations; the selected row is marked Current.",
    "Use Back to workspace to return to the active workspace console.",
    "Create a workspace or open a workspace's dedicated administration page.",
    "Use an instance-admin token for workspace lifecycle operations; workspace tokens remain scoped to one workspace.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click(".sidebarFooter .workspaceSwitcherTrigger");
    await page.click(".sidebarFooter .workspaceManageLink");
    await page.waitForSelector("#workspaceRegistry tbody tr");
    await page.waitForText("#workspaceRegistry", "Operations");
    await page.waitForText("#workspaceRegistry", "Current");
    await page.waitForText("#workspaceRegistry", "Switch");
    await capture(this.id);
  },
};
