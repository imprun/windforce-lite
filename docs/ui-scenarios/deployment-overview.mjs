export default {
  order: 2,
  id: "deployment-overview",
  title: "Review deployment inventory",
  description: "Use the deployment console to choose a registered FCode, inspect its current release, and check deployment readiness.",
  screenshot: "docs/assets/ui/deployment-overview.png",
  guide: [
    "Open the deployment management console.",
    "Use the sidebar to move between deployment, source, release, and audit work areas.",
    "Use the release candidate table to compare registered FCodes.",
    "Select a row to inspect the release brief, readiness checks, and latest audit entries.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForText("#sourceList", "FCode release candidates");
    await page.waitForSelector("#sourceList .tableRow");
    await page.waitForSelector("#sourceDetail");
    await capture(this.id);
  },
};
