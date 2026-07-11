export default {
  order: 5,
  id: "deploy-app",
  title: "Deploy a source",
  description: "Use the Deployments view to publish the selected source as the active Windforce app contract.",
  screenshot: "docs/assets/ui/deploy-app.png",
  guide: [
    "Open the deployment management console.",
    "Select a registered source.",
    "Open the deploy dialog.",
    "Confirm repository, branch, subpath, and current release.",
    "Add a deployment note and deploy the source.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForSelector("#sourceList .tableRow");
    await page.click("#sourceList .compactButton.primary");
    await page.waitForSelector("#deploySourceDialog");
    await page.fill("#deploySourceMessage", "UI guide deployment");
    await capture(this.id);
    await page.click("#deploySourceDialog .button.primary");
    await page.waitForText("#toast", "Deployed");
  },
};
