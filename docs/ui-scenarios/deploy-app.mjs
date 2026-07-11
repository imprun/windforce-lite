export default {
  order: 3,
  id: "deploy-app",
  title: "Deploy an app source",
  description: "Use the Deployments view to register an app source and deploy the active Windforce manifest.",
  screenshot: "docs/assets/ui/deploy-app.png",
  guide: [
    "Open the deployment management console.",
    "Select the FCode source to deploy.",
    "Confirm readiness and current release metadata in the selected source detail.",
    "Use Deploy to open a confirmation dialog.",
    "Type the source name and add an audit note before publishing the active app contract.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForSelector("#sourceList .tableRow");
    await page.click("#deploySelectedSource");
    await page.waitForSelector("#deployDialog");
    await page.fill("#deployConfirmInput", "echo");
    await page.fill("#deployMessage", "UI guide deployment");
    await capture(this.id);
    await page.click("#deployDialog .button.primary");
    await page.waitForText("#notice", "Deployed");
  },
};
