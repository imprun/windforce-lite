export const recentWindows = [
  { label: "1h", seconds: 3600 },
  { label: "24h", seconds: 86400 },
  { label: "7d", seconds: 604800 },
] as const;

export type StatTone = "waiting" | "running" | "good" | "critical" | "serious" | "neutral";

export function StatTile({
  label,
  value,
  tone,
}: {
  label: string;
  value: number | string | undefined;
  tone: StatTone;
}) {
  return (
    <div className="statTile">
      <span className={`statDot dot-${tone}`} aria-hidden="true" />
      <div>
        <p className="statValue">{value ?? "—"}</p>
        <p className="statLabel">{label}</p>
      </div>
    </div>
  );
}

export function WindowSelector({
  value,
  onChange,
}: {
  value: number;
  onChange: (seconds: number) => void;
}) {
  return (
    <div className="segmented" role="group" aria-label="Recent window">
      {recentWindows.map((item) => (
        <button
          key={item.label}
          type="button"
          className={item.seconds === value ? "segment active" : "segment"}
          onClick={() => onChange(item.seconds)}
        >
          {item.label}
        </button>
      ))}
    </div>
  );
}

export function windowLabel(seconds: number): string {
  return recentWindows.find((item) => item.seconds === seconds)?.label || "24h";
}
