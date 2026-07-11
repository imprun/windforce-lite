export default {
  order: 3,
  id: "source-detail",
  title: "Inspect a source detail sheet",
  description: "Open a registered source sheet to review source registration, active contract, readiness, source snapshot, and audit evidence.",
  screenshot: "docs/assets/ui/source-detail.png",
  guide: [
    "Open the deployment management console.",
    "Open a registered source detail sheet.",
    "Review the active worker contract and exposed actions.",
    "Check readiness signals before deploying.",
    "Inspect the active source snapshot and latest audit entries.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForSelector("#sourceList .tableRow");
    await page.click("#sourceList .rowButtons .button");
    await page.waitForSelector("#sourceDetailPage");
    await page.waitForText("#sourceDetailPage", "Active contract");
    await page.waitForText("#sourceDetailPage", "Release audit");
    await page.waitForText("#sourceSnapshot", "windforce.json");
    await capture(this.id);
  },
};
