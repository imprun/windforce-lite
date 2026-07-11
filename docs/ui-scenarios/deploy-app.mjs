export default {
  order: 5,
  id: "deploy-app",
  title: "Publish app release",
  description: "Use the Apps view to publish the selected app as the active worker contract.",
  screenshot: "docs/assets/ui/deploy-app.png",
  guide: [
    "Open the app release console.",
    "Select a registered app.",
    "Open the release dialog.",
    "Confirm repository, branch, subpath, and current release.",
    "Add a release note and publish the app.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#appList .compactButton.primary");
    await page.waitForSelector("#deploySourceDialog");
    await page.fill("#deploySourceMessage", "UI guide release");
    await capture(this.id);
    await page.click("#deploySourceDialog .button.primary");
    await page.waitForText("#toast", "Published");
  },
};
