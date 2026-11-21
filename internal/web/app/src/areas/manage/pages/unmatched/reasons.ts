import type { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import type { FolderHint, UnmatchedGroup } from "./groups";

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

/**
 * groupProblem says in plain words why a group did not match and, when its
 * folder name explains it, what would help. Scores appear only as supporting
 * detail of the below-threshold sentence, never as the explanation itself.
 */
export function groupProblem(
  t: ReturnType<typeof useT>,
  group: UnmatchedGroup,
  hint: FolderHint | undefined,
): { why: string; hint: string } {
  const why = (() => {
    switch (group.reason) {
      case "ambiguous":
        return t("unmatchedWhyAmbiguous");
      case "below-threshold":
        return group.candidateTitle
          ? t("unmatchedWhyBelow", {
              title: group.candidateTitle,
              confidence: percent(group.confidence),
              threshold: percent(group.threshold),
            })
          : t("unmatchedWhyBelowUntitled", {
              confidence: percent(group.confidence),
              threshold: percent(group.threshold),
            });
      case "no-candidates":
        return t("unmatchedWhyNone");
      case "unscored":
        return t("unmatchedWhyManual");
      default:
        return t("unmatchedWhyUnknown");
    }
  })();
  if (hint?.qualifier) {
    return { why, hint: t("unmatchedHintQualifier", { qualifier: hint.qualifier }) };
  }
  if (group.reason === "ambiguous") {
    if (hint?.year) return { why, hint: t("unmatchedHintYear", { year: String(hint.year) }) };
    const name = hint?.title || group.candidateTitle || group.items[0]?.title || "";
    if (name) return { why, hint: t("unmatchedHintAddYear", { example: `${name} (YYYY)` }) };
  }
  return { why, hint: "" };
}
