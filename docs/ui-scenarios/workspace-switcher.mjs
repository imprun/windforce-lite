export default {
  order: 9,
  id: "workspace-switcher",
  title: "Switch workspace context",
  description:
    "The workspace switcher identifies the current scope, lists available workspaces, and provides the entry point to instance workspace administration.",
  screenshot: "docs/assets/ui/workspace-switcher.png",
  guide: [
    "Open the workspace control at the bottom of the sidebar.",
    "Select a workspace to change the active application and monitoring scope.",
    "Choose Manage workspaces to create workspaces or manage identity, access, audit, and lifecycle settings.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click(".sidebarFooter .workspaceSwitcherTrigger");
    await page.waitForSelector(".workspacePopover");
    await capture(this.id);
  },
};
