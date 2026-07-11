export default {
  order: 3,
  id: "deploy-app",
  title: "Deploy an app source",
  description: "Use the Deployments view to register an app source and deploy the active Windforce manifest.",
  screenshot: "docs/assets/ui/deploy-app.png",
  guide: [
    "Open the deployment management console.",
    "Choose the Git authentication mode: no authentication, personal access token, or username/password.",
    "Register the Git source with branch and subpath. Registration validates repository access, the branch, manifest, schemas, and lockfile before saving.",
    "Open the registered FCode detail and confirm the source name before deployment.",
    "Use Deploy to materialize the current commit and publish the active app contract with audit metadata.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.evaluate(() => {
      localStorage.setItem("wf.actor", "ui-guide@example.test");
    });
    await page.goto();
    await page.waitForSelector("#sourceList .listRow");
    await page.click("#sourceDetail .button.primary");
    await page.waitForSelector("#deployDialog");
    await page.fill("#deployConfirmInput", "echo");
    await page.fill("#deployMessage", "UI guide deployment");
    await page.click("#deployDialog .button.primary");
    await page.waitForText("#notice", "Deployed");
    await capture(this.id);
  },
};
