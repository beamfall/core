import type { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

/**
 * The ADR-0068 §A failure-reason taxonomy as the server sends it. These are
 * shape words on the wire (invariant #1); the Console owns their prose.
 */
const REASON_STRING_KEYS: Record<string, StringKey> = {
  "no-candidates": "unmatchedReasonNoCandidates",
  "below-threshold": "unmatchedReasonBelowThreshold",
  ambiguous: "unmatchedReasonAmbiguous",
  unscored: "unmatchedReasonUnscored",
};

/**
 * reasonLabel renders one taxonomy word. An unknown word is passed through
 * verbatim rather than dropped: a server that learns a new reason before this
 * client does must still tell the operator something true.
 */
export function reasonLabel(t: ReturnType<typeof useT>, reason: string | undefined): string {
  if (!reason) return "";
  const key = REASON_STRING_KEYS[reason];
  return key ? t(key) : reason;
}

/** percent renders a [0,1] confidence as whole-percent text. */
export function percent(value: number): string {
  return `${Math.round(value * 100)}%`;
}
