import { describe, expect, test } from "bun:test";
import { settingsNavItems } from "./SettingsNav";

describe("settingsNavItems", () => {
  test("keeps operational settings before read-only information", () => {
    expect(settingsNavItems.map((item) => item.label)).toEqual([
      "General",
      "Provisioning",
      "Webhooks",
      "Info",
    ]);
  });
});
