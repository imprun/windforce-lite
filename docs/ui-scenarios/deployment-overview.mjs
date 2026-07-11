export default {
  order: 2,
  id: "deployment-overview",
  title: "Manage app releases",
  description: "Use the app console to inspect registered apps, active contracts, readiness, and release audit evidence.",
  screenshot: "docs/assets/ui/deployment-overview.png",
  guide: [
    "Open the app release console.",
    "Use the sidebar to move between Apps, Contracts, History, and Settings.",
    "Use the app table to compare registered apps.",
    "Open an app sheet for release evidence.",
    "Use the active contracts table to confirm what workers can execute.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForText("#appList", "Registered apps");
    await page.waitForSelector("#appList .tableRow");
    await page.waitForText("#deploymentOverview", "Worker-visible apps");
    await capture(this.id);
  },
};
