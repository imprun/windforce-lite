import { describe, expect, test } from "bun:test";
import { formatSystemInfoValue } from "./SettingsInfoPage";

describe("formatSystemInfoValue", () => {
  test("uses neutral language for disabled boolean settings", () => {
    expect(formatSystemInfoValue(true)).toBe("Enabled");
    expect(formatSystemInfoValue(false)).toBe("Not enabled");
  });

  test("keeps empty values compact", () => {
    expect(formatSystemInfoValue("")).toBe("—");
    expect(formatSystemInfoValue(null)).toBe("—");
  });
});
