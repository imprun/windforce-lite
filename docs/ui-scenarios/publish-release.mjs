export default {
  order: 4,
  id: "publish-release",
  title: "Publish a release",
  description:
    "Publish Release prepares the latest synchronized source and publishes it as the worker-visible contract, recorded with the audit actor.",
  screenshot: "docs/assets/ui/publish-release.png",
  guide: [
    "Open an app and click Publish Release.",
    "Compare Active release with Latest synchronized.",
    "Add a release note for the audit trail.",
    "Publish the latest synchronized revision; the release history records the actor, commit, and note.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => localStorage.setItem("wf.actor", "ui-guide@example.test"));
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#appList .tableRow");
    await page.waitForSelector("#publishReleaseButton");
    await page.waitForSelector("#publishReleaseButton:not([disabled])");
    await page.click("#publishReleaseButton");
    await page.waitForSelector("#publishReleaseDialog");
    await page.fill("#publishReleaseMessage", "Ship the new echo contract");
    await capture(this.id);
  },
};
