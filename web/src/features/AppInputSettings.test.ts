import { describe, expect, test } from "bun:test";
import { formatInputSettingValue } from "./InputSettingScopeList";

describe("formatInputSettingValue", () => {
  test("preserves JSON scalar types", () => {
    expect(formatInputSettingValue("30")).toBe('"30"');
    expect(formatInputSettingValue(30)).toBe("30");
    expect(formatInputSettingValue(false)).toBe("false");
    expect(formatInputSettingValue(null)).toBe("null");
  });

  test("formats structured values for direct inspection", () => {
    expect(formatInputSettingValue({ region: "kr", retries: 2 })).toBe(`{
  "region": "kr",
  "retries": 2
}`);
  });
});
