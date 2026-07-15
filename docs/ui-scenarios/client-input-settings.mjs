export default {
  id: "client-input-settings",
  title: "Manage client input settings",
  description: "Review app- and action-specific values applied for one external client.",
  guide: [
    "Open Client Registry.",
    "Select an external client.",
    "Open a settings row to review its JSON values and locked keys.",
  ],
  screenshot: "docs/assets/ui/client-input-settings.png",
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Client Registry");
    await page.clickText("Example Retailer");
    await page.waitForSelector("#clientInputSettings tbody tr");
    await page.waitForSelector("#clientInputSettingsAudit tbody tr");
    await page.click('#clientInputSettings button[aria-label="Edit input settings"]');
    await page.waitForSelector('input[value="response_mode"]');
    await page.waitForSelector('button[aria-label="Unlock input key"]');
    await capture("client-input-settings");
  },
};
