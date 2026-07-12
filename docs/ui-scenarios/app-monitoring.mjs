export default {
  order: 6,
  id: "app-monitoring",
  title: "Monitor one app",
  description:
    "The app detail Monitoring tab narrows the workspace job aggregates to a single app: queued and running now, plus completed, failed, canceled, and the failure rate in the selected window.",
  screenshot: "docs/assets/ui/app-monitoring.png",
  guide: [
    "Open an app and switch to the Monitoring tab.",
    "Read the tiles for this app's queued, running, and windowed completed/failed/canceled counts.",
    "Switch the window between 1h, 24h, and 7d.",
    "Watch the failure rate; the workspace-wide picture lives on the Monitoring page.",
  ],
  async run({ page, capture }) {
    await page.goto();
    await page.waitForSelector("#appList .tableRow");
    await page.click("#appList .tableRow");
    await page.waitForSelector(".tabBar");
    await page.click(".tabBar .tab[href$='/monitoring']");
    await page.waitForSelector("#appMonitoring");
    await capture(this.id);
  },
};
