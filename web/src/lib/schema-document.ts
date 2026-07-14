export type SchemaField = {
  name: string;
  title?: string;
  type: string;
  required: boolean;
  description?: string;
  format?: string;
  enumValues?: unknown[];
  constValue?: unknown;
  defaultValue?: unknown;
  hasDefault: boolean;
};

export type SchemaExample = {
  value: unknown;
  source: "declared" | "generated";
};

export type SchemaDocument = {
  title?: string;
  description?: string;
  type: string;
  fields: SchemaField[];
  example: SchemaExample;
};

type JSONRecord = Record<string, unknown>;

export function describeSchema(raw: unknown): SchemaDocument {
  const schema = asRecord(raw);
  if (!schema) {
    return { type: "JSON", fields: [], example: { value: {}, source: "generated" } };
  }

  const properties = asRecord(schema.properties);
  const required = new Set(
    Array.isArray(schema.required) ? schema.required.filter((item): item is string => typeof item === "string") : [],
  );
  const fields = properties
    ? Object.entries(properties).map(([name, value]) => describeField(name, value, required.has(name)))
    : [];

  return {
    title: stringValue(schema.title),
    description: stringValue(schema.description),
    type: schemaType(schema),
    fields,
    example: schemaExample(schema),
  };
}

export function formatSchemaValue(value: unknown): string {
  if (typeof value === "string") return JSON.stringify(value);
  const encoded = JSON.stringify(value);
  return encoded === undefined ? String(value) : encoded;
}

function describeField(name: string, raw: unknown, required: boolean): SchemaField {
  const schema = asRecord(raw) || {};
  const hasDefault = Object.prototype.hasOwnProperty.call(schema, "default");
  return {
    name,
    title: stringValue(schema.title),
    type: schemaType(schema),
    required,
    description: stringValue(schema.description),
    format: stringValue(schema.format),
    enumValues: Array.isArray(schema.enum) ? schema.enum : undefined,
    constValue: Object.prototype.hasOwnProperty.call(schema, "const") ? schema.const : undefined,
    defaultValue: hasDefault ? schema.default : undefined,
    hasDefault,
  };
}

function schemaExample(schema: JSONRecord): SchemaExample {
  if (Array.isArray(schema.examples) && schema.examples.length > 0) {
    return { value: schema.examples[0], source: "declared" };
  }
  if (Object.prototype.hasOwnProperty.call(schema, "example")) {
    return { value: schema.example, source: "declared" };
  }
  return { value: generatedExample(schema), source: "generated" };
}

function generatedExample(schema: JSONRecord): unknown {
  if (Object.prototype.hasOwnProperty.call(schema, "const")) return schema.const;
  if (Object.prototype.hasOwnProperty.call(schema, "default")) return schema.default;
  if (Array.isArray(schema.enum) && schema.enum.length > 0) return schema.enum[0];

  const properties = asRecord(schema.properties);
  if (schemaType(schema) === "object" || properties) {
    const required = new Set(
      Array.isArray(schema.required) ? schema.required.filter((item): item is string => typeof item === "string") : [],
    );
    const example: JSONRecord = {};
    for (const [name, value] of Object.entries(properties || {})) {
      const property = asRecord(value) || {};
      if (
        required.has(name) ||
        Object.prototype.hasOwnProperty.call(property, "const") ||
        Object.prototype.hasOwnProperty.call(property, "default")
      ) {
        example[name] = generatedExample(property);
      }
    }
    return example;
  }

  switch (schemaType(schema)) {
    case "array":
      return [];
    case "boolean":
      return false;
    case "integer":
    case "number":
      return 0;
    case "null":
      return null;
    default:
      return "string";
  }
}

function schemaType(schema: JSONRecord): string {
  if (typeof schema.type === "string" && schema.type.trim()) return schema.type.trim();
  if (Array.isArray(schema.type)) {
    const types = schema.type.filter((item): item is string => typeof item === "string" && item.trim() !== "");
    if (types.length > 0) return types.join(" | ");
  }
  if (asRecord(schema.properties)) return "object";
  if (schema.items !== undefined) return "array";
  return "JSON";
}

function asRecord(value: unknown): JSONRecord | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  return value as JSONRecord;
}

function stringValue(value: unknown): string | undefined {
  const text = typeof value === "string" ? value.trim() : "";
  return text || undefined;
}
