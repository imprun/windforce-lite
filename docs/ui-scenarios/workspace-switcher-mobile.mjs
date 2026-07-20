export default {
  order: 9.5,
  id: "workspace-switcher-mobile",
  title: "Switch workspace on a narrow screen",
  description:
    "The current workspace remains visible below the page title and opens the same context and administration menu on narrow screens.",
  screenshot: "docs/assets/ui/workspace-switcher-mobile.png",
  viewport: { width: 390, height: 844 },
  guide: [
    "Open the workspace control below the page title.",
    "Choose another workspace or open Manage workspaces without returning to desktop navigation.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click(".mobileWorkspaceContext .workspaceSwitcherTrigger");
    await page.waitForSelector(".mobileWorkspaceContext .workspacePopover");
    await capture(this.id);
  },
};
