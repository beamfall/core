import { useEffect } from "react";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import type { AutoSkipSegment } from "../lib/transport";

const AUTO_DISMISS_MS = 6000;

function skippedLabelKey(segmentKind: string): StringKey {
  if (segmentKind === "intro") return "skippedIntro";
  if (segmentKind === "recap") return "skippedRecap";
  if (segmentKind === "credits") return "skippedCredits";
  return "segment";
}

// The auto-skip confirmation, ported from the manual skip-segment control
// (ADR-0048 Decision 6) but built from the same floating-card primitive as
// the Up Next card (bf-up-next / bf-up-next-media / bf-up-next-actions /
// bf-control) — no new visual language or component styles. role="status" +
// aria-live="polite" is the screen-reader announcement (ADR-0014 §4
// skip-segment announcements); Undo is a single ordinary focusable button and
// the card auto-dismisses itself, so it never traps focus.
export function SkippedOverlay({
  segment,
  onUndo,
  onDismiss,
}: {
  segment: AutoSkipSegment;
  onUndo: () => void;
  onDismiss: () => void;
}) {
  const t = useT();

  useEffect(() => {
    const timer = window.setTimeout(onDismiss, AUTO_DISMISS_MS);
    return () => window.clearTimeout(timer);
    // Re-arms the auto-dismiss timer whenever a *new* segment is skipped
    // (the transport re-renders with the same overlay instance in place).
  }, [segment, onDismiss]);

  return (
    <div className="bf-up-next" role="status" aria-live="polite">
      <span className="bf-up-next-media">{t(skippedLabelKey(segment.segmentKind))}</span>
      <div className="bf-up-next-actions">
        <button type="button" className="bf-control is-primary" onClick={onUndo}>
          {t("autoSkipUndo")}
        </button>
      </div>
    </div>
  );
}
