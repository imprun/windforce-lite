import { describe, expect, test } from "bun:test";
import { inputSettingDefinitions, validateInputSettingValue } from "./input-setting-schema";

describe("inputSettingDefinitions", () => {
  test("combines request fields and operator-only settings", () => {
    const definitions = inputSettingDefinitions(
      { type: "object", properties: { OWNERNAME: { type: "string", description: "Vehicle owner" } } },
      {
        type: "object",
        properties: {
          GOV24_VEHICLE_LEDGER_ROUTE: {
            type: "object",
            title: "Vehicle ledger route",
            required: ["gap", "eul"],
            properties: {
              gap: { type: "string", enum: ["gov24-plus", "gov24"] },
              eul: { type: "string", enum: ["gov24-plus", "gov24"] },
            },
            examples: [{ gap: "gov24-plus", eul: "gov24" }],
          },
        },
      },
    );

    expect(definitions.map(({ key, source }) => ({ key, source }))).toEqual([
      { key: "GOV24_VEHICLE_LEDGER_ROUTE", source: "operator" },
      { key: "OWNERNAME", source: "request" },
    ]);
    expect(definitions[0].fields.map((field) => field.name)).toEqual(["gap", "eul"]);
    expect(definitions[0].example).toEqual({ gap: "gov24-plus", eul: "gov24" });
  });

  test("operator definition takes precedence for a duplicate key", () => {
    const definitions = inputSettingDefinitions(
      { type: "object", properties: { MODE: { type: "string" } } },
      { type: "object", properties: { MODE: { type: "string", description: "Operator choice" } } },
    );
    expect(definitions).toHaveLength(1);
    expect(definitions[0].source).toBe("operator");
    expect(definitions[0].description).toBe("Operator choice");
  });
});

describe("validateInputSettingValue", () => {
  const definition = inputSettingDefinitions(
    {},
    {
      type: "object",
      properties: {
        ROUTE: {
          type: "object",
          required: ["gap", "eul"],
          additionalProperties: false,
          properties: {
            gap: { type: "string", enum: ["gov24-plus", "gov24"] },
            eul: { type: "string", enum: ["gov24-plus", "gov24"] },
          },
        },
      },
    },
  )[0];

  test("accepts a documented value", () => {
    expect(validateInputSettingValue(definition, { gap: "gov24-plus", eul: "gov24" })).toBeUndefined();
  });

  test("explains missing, unknown, and invalid values", () => {
    expect(validateInputSettingValue(definition, { gap: "gov24-plus" })).toBe("ROUTE.eul is required.");
    expect(validateInputSettingValue(definition, { gap: "other", eul: "gov24" })).toContain("must be one of");
    expect(validateInputSettingValue(definition, { gap: "gov24-plus", eul: "gov24", extra: true })).toBe(
      "ROUTE.extra is not an allowed field.",
    );
  });
});
