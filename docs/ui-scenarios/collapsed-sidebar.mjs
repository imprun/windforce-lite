export default {
  order: 2,
  id: "collapsed-sidebar",
  title: "Collapse navigation",
  description: "Collapse the sidebar while keeping app release work visible.",
  screenshot: "docs/assets/ui/collapsed-sidebar.png",
  guide: [
    "Click the sidebar collapse control.",
    "Use the compact navigation rail to keep app release work visible.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.click("#toggleSidebar");
    await page.waitForSelector(".appShell.sidebarCollapsed");
    await capture(this.id);
  },
};
