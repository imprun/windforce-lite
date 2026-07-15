export default {
  order: 10,
  id: "collapse-sidebar",
  title: "Collapse the sidebar",
  description:
    "The sidebar collapses to an icon rail so wide tables get the full viewport. The choice is remembered in the browser.",
  screenshot: "docs/assets/ui/collapse-sidebar.png",
  guide: [
    "Click the collapse control beside the product title at the top of the sidebar.",
    "Navigate with the icon rail; hover shows each destination.",
    "Click the control again to expand the sidebar.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#sidebarToggle");
    await page.waitForSelector(".sidebarCollapsed");
    await capture(this.id);
  },
};
