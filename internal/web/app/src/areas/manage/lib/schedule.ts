import type { ScheduleTriggerInfo, ScheduleTriggerInput } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

// Pure helpers for the structured Schedule trigger vocabulary (ADR-0032),
// ported verbatim from internal/web/static/app.js (renderTrigger / triggerFields
// / triggerFromEditor). The closed trigger kinds are "every" | "daily" | "weekly";
// the server is the authority — these only shape the editor and the summary.

export type TriggerKind = "every" | "daily" | "weekly";

/** The weekday option keys (0 = Sunday .. 6 = Saturday) and their string keys. */
export const WEEKDAY_KEYS: StringKey[] = [
  "sunday",
  "monday",
  "tuesday",
  "wednesday",
  "thursday",
  "friday",
  "saturday",
];

function zeroPad(value: number): string {
  return String(value).padStart(2, "0");
}

/**
 * A compact, localized one-line summary of a trigger. `t` resolves string keys;
 * `weekdayLabel` resolves a weekday index to its display label.
 */
export function renderTrigger(
  trigger: ScheduleTriggerInfo | null | undefined,
  t: (key: StringKey) => string,
): string {
  if (!trigger) return "";
  if (trigger.kind === "every") {
    const unit = (trigger.unit === "minutes" ? t("minutes") : t("hours")).toLowerCase();
    return `${t("every")} ${trigger.n} ${unit}`;
  }
  const time = `${zeroPad(trigger.hour)}:${zeroPad(trigger.minute)}`;
  if (trigger.kind === "weekly") {
    const weekday = t(WEEKDAY_KEYS[trigger.weekday] ?? "sunday");
    return `${t("weekly")} ${weekday} ${time}`;
  }
  return `${t("daily")} ${time}`;
}

/** The editable fields a trigger kind exposes — drives the editor form. */
export type TriggerFields = {
  n: number;
  unit: "minutes" | "hours";
  weekday: number;
  hour: number;
  minute: number;
};

/** Seed the editor fields from a trigger, falling back to app.js defaults. */
export function triggerFieldsFrom(trigger: ScheduleTriggerInfo | null | undefined): TriggerFields {
  return {
    n: trigger?.n || 1,
    unit: trigger?.unit === "minutes" ? "minutes" : "hours",
    weekday: trigger?.weekday || 0,
    hour: trigger?.hour || 0,
    minute: trigger?.minute || 0,
  };
}

/** Build the mutation input for a given kind from the editor fields. */
export function triggerInput(kind: TriggerKind, fields: TriggerFields): ScheduleTriggerInput {
  if (kind === "every") {
    return { kind, n: fields.n, unit: fields.unit };
  }
  if (kind === "weekly") {
    return {
      kind,
      weekday: fields.weekday,
      hour: fields.hour,
      minute: fields.minute,
    };
  }
  return { kind, hour: fields.hour, minute: fields.minute };
}

/** Format an ISO date/time for a schedule detail line; "" when unparseable. */
export function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString();
}
