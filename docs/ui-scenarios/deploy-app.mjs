export default {
  order: 3,
  id: "deploy-app",
  title: "Deploy an app source",
  description: "Use the Deployments view to register an app source and deploy the active Windforce manifest.",
  screenshot: "docs/assets/ui/deploy-app.png",
  guide: [
    "Open the Deployments view.",
    "Choose the Git authentication mode: no authentication, personal access token, or username/password.",
    "Register the Git source with branch and subpath. Registration validates repository access, the branch, manifest, schemas, and lockfile before saving.",
    "Use Deploy to materialize the current commit and publish the active app contract.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Deployments");
    await page.waitForSelector("#sourceList .table-row");
    await page.click("[data-deploy-id]");
    await page.waitForText("#notice", "Deployed");
    await capture(this.id);
  },
};
