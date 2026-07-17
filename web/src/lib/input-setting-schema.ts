import { describeSchema, formatSchemaValue, type SchemaField } from "./schema-document";

type JSONRecord = Record<string, unknown>;

export type InputSettingDefinition = {
  key: string;
  source: "operator" | "request";
  title?: string;
  description?: string;
  type: string;
  fields: SchemaField[];
  enumValues?: unknown[];
  constValue?: unknown;
  example: unknown;
  schema: JSONRecord;
};

export function inputSettingDefinitions(inputSchema: unknown, operatorSettingsSchema: unknown): InputSettingDefinition[] {
  const definitions = new Map<string, InputSettingDefinition>();
  addDefinitions(definitions, inputSchema, "request");
  addDefinitions(definitions, operatorSettingsSchema, "operator");
  return [...definitions.values()].sort((left, right) => {
    if (left.source !== right.source) return left.source === "operator" ? -1 : 1;
    return left.key.localeCompare(right.key);
  });
}

export function formatInputSettingExample(value: unknown): string {
  if (typeof value === "string") return JSON.stringify(value);
  return JSON.stringify(value, null, 2) ?? String(value);
}

export function validateInputSettingValue(definition: InputSettingDefinition, value: unknown): string | undefined {
  return validateSchemaValue(definition.schema, value, definition.key);
}

function addDefinitions(
  definitions: Map<string, InputSettingDefinition>,
  rawSchema: unknown,
  source: InputSettingDefinition["source"],
) {
  const schema = asRecord(rawSchema);
  const properties = asRecord(schema?.properties);
  if (!properties) return;
  for (const [key, rawProperty] of Object.entries(properties)) {
    const property = asRecord(rawProperty);
    if (!property) continue;
    const document = describeSchema(property);
    definitions.set(key, {
      key,
      source,
      title: stringValue(property.title),
      description: stringValue(property.description),
      type: document.type,
      fields: document.fields,
      enumValues: Array.isArray(property.enum) ? property.enum : undefined,
      constValue: Object.prototype.hasOwnProperty.call(property, "const") ? property.const : undefined,
      example: document.example.value,
      schema: property,
    });
  }
}

function validateSchemaValue(schema: JSONRecord, value: unknown, path: string): string | undefined {
  if (Object.prototype.hasOwnProperty.call(schema, "const") && !sameJSON(schema.const, value)) {
    return `${path} must be ${formatSchemaValue(schema.const)}.`;
  }
  if (Array.isArray(schema.enum) && !schema.enum.some((candidate) => sameJSON(candidate, value))) {
    return `${path} must be one of ${schema.enum.map(formatSchemaValue).join(", ")}.`;
  }

  const type = schema.type;
  if (typeof type === "string" && !matchesType(type, value)) {
    return `${path} must be ${article(type)} ${type}.`;
  }
  if (Array.isArray(type)) {
    const allowed = type.filter((item): item is string => typeof item === "string");
    if (allowed.length > 0 && !allowed.some((candidate) => matchesType(candidate, value))) {
      return `${path} must be one of these types: ${allowed.join(", ")}.`;
    }
  }

  const properties = asRecord(schema.properties);
  if (properties && isRecord(value)) {
    const required = new Set(
      Array.isArray(schema.required) ? schema.required.filter((item): item is string => typeof item === "string") : [],
    );
    for (const key of required) {
      if (!Object.prototype.hasOwnProperty.call(value, key)) return `${path}.${key} is required.`;
    }
    for (const [key, nestedValue] of Object.entries(value)) {
      const property = asRecord(properties[key]);
      if (!property) {
        if (schema.additionalProperties === false) return `${path}.${key} is not an allowed field.`;
        continue;
      }
      const error = validateSchemaValue(property, nestedValue, `${path}.${key}`);
      if (error) return error;
    }
  }

  if (Array.isArray(value)) {
    const items = asRecord(schema.items);
    if (items) {
      for (let index = 0; index < value.length; index += 1) {
        const error = validateSchemaValue(items, value[index], `${path}[${index}]`);
        if (error) return error;
      }
    }
  }
  return undefined;
}

function matchesType(type: string, value: unknown): boolean {
  switch (type) {
    case "object":
      return isRecord(value);
    case "array":
      return Array.isArray(value);
    case "string":
      return typeof value === "string";
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    case "number":
      return typeof value === "number" && Number.isFinite(value);
    case "boolean":
      return typeof value === "boolean";
    case "null":
      return value === null;
    default:
      return true;
  }
}

function sameJSON(left: unknown, right: unknown): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}

function article(type: string): string {
  return /^[aeiou]/i.test(type) ? "an" : "a";
}

function asRecord(value: unknown): JSONRecord | null {
  return isRecord(value) ? value : null;
}

function isRecord(value: unknown): value is JSONRecord {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function stringValue(value: unknown): string | undefined {
  const text = typeof value === "string" ? value.trim() : "";
  return text || undefined;
}
