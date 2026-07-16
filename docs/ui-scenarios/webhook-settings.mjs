export default {
  order: 12,
  id: "webhook-settings",
  title: "Configure a release webhook",
  description:
    "Webhook detail keeps receiver configuration, app scope, enablement, secret rotation, and deletion controls on a full page.",
  screenshot: "docs/assets/ui/webhook-settings.png",
  guide: [
    "Open a webhook from Settings.",
    "Review its masked receiver, event type, status, and last operator update.",
    "Change its name, replace the endpoint, enable or disable delivery, or narrow the app scope.",
    "Rotate the signing secret only when the receiver can be updated immediately.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.clickText("Settings");
    await page.clickText("Webhooks");
    await page.waitForSelector("#webhookList tbody tr");
    await page.clickText("Release notifications");
    await page.waitForSelector("#saveWebhookButton");
    await capture(this.id);
  },
};
