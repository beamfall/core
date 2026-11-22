// Presentation shared by the Console's play reporting and a viewer's own
// watch history in My Data (CORE-1703).
import type { PlayMethod } from "@/lib/graphql/types";
import type { StringKey } from "@/strings";

/** Seconds as a short duration: 2 h 05 min, 14 min, 40 s. */
export function watched(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h} h ${String(m).padStart(2, "0")} min`;
  if (m > 0) return `${m} min`;
  return `${s} s`;
}

const PLAY_METHOD_LABELS: Record<PlayMethod, StringKey> = {
  direct: "directPlay",
  remux: "directStream",
  transcode: "serverTranscode",
  unknown: "playMethodUnknown",
};

/** The label for how a play was delivered. */
export function playMethodLabel(method: PlayMethod): StringKey {
  return PLAY_METHOD_LABELS[method] ?? "playMethodUnknown";
}

/** A stats day (yyyy-mm-dd, already in local time) as a short local date. */
export function playDayLabel(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  if (!y || !m || !d) return day;
  return new Date(y, m - 1, d).toLocaleDateString([], { dateStyle: "medium" });
}
