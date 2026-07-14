import { describe, expect, test } from "bun:test";
import { describeSchema, formatSchemaValue } from "./schema-document";

describe("describeSchema", () => {
  test("uses a declared example and exposes field documentation", () => {
    const schema = describeSchema({
      title: "Session request",
      type: "object",
      required: ["mode", "account"],
      properties: {
        mode: { type: "string", enum: ["login", "refresh"], default: "login", description: "Session operation" },
        account: { type: "string", title: "Account ID" },
        retries: { type: "integer", const: 1 },
      },
      examples: [{ mode: "login", account: "example-account", retries: 1 }],
    });

    expect(schema.title).toBe("Session request");
    expect(schema.example).toEqual({
      source: "declared",
      value: { mode: "login", account: "example-account", retries: 1 },
    });
    expect(schema.fields).toEqual([
      {
        name: "mode",
        type: "string",
        required: true,
        description: "Session operation",
        enumValues: ["login", "refresh"],
        defaultValue: "login",
        hasDefault: true,
      },
      { name: "account", title: "Account ID", type: "string", required: true, hasDefault: false },
      { name: "retries", type: "integer", required: false, constValue: 1, hasDefault: false },
    ]);
  });

  test("generates a structural example when no example is declared", () => {
    const schema = describeSchema({
      type: "object",
      required: ["name", "enabled"],
      properties: {
        name: { type: "string" },
        enabled: { type: "boolean" },
        limit: { type: "integer", default: 25 },
        unused: { type: "string" },
      },
    });

    expect(schema.example).toEqual({
      source: "generated",
      value: { name: "string", enabled: false, limit: 25 },
    });
    expect(formatSchemaValue("login")).toBe('"login"');
    expect(formatSchemaValue(["login", "refresh"])).toBe('["login","refresh"]');
  });
});
