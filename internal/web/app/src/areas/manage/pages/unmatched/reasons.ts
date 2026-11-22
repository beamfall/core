import { percent } from "@/components/assign/matchLabels";
import type { useT } from "@/lib/i18n";

import type { FolderHint, UnmatchedGroup } from "./groups";

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
