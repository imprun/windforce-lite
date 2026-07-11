export function shortID(value: unknown, size = 12): string {
  const text = String(value || "");
  if (!text) return "-";
  return text.length <= size ? text : `${text.slice(0, size)}...`;
}

export function formatDate(value: unknown): string {
  if (!value) return "-";
  const date = new Date(String(value));
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString();
}

export function compactRecord<T extends Record<string, unknown>>(record: T): Partial<T> {
  return Object.fromEntries(
    Object.entries(record).filter(([, value]) => value !== undefined && value !== null && value !== ""),
  ) as Partial<T>;
}
