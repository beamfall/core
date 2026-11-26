import { EyeClosed, ScanEye } from "lucide-react";
import type { MouseEvent } from "react";
import { ActionTooltip } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n";
import { artworkBucketURL } from "../lib/artwork";
import {
  type SpoilerState,
  setContainerRevealed,
  toggleItemReveal,
  useSpoilerGuard,
} from "../lib/spoilerGuard";

// The spoiler guard's pieces for child rows and cards (CORE-3057): the
// placeholder drawn instead of a guarded still, the per-item reveal toggle
// and the per-container toggle. Reveals change opacity only, and not at all
// under reduced motion (entity-detail.css).

/**
 * The placeholder for a guarded still: the container's art dimmed, or the
 * empty tile, with a closed-eye mark. A click reveals the item; the reveal
 * button is the keyboard and assistive path, so this stays decorative.
 */
export function SpoilerStill({ itemID, art }: { itemID: string; art?: string }) {
  function reveal(event: MouseEvent) {
    // The still sits inside the row's link: reveal instead of opening the item.
    event.preventDefault();
    event.stopPropagation();
    toggleItemReveal(itemID);
  }
  return (
    <span className="bf-spoiler-still" data-testid="spoiler-still" onClick={reveal}>
      {art && <img src={artworkBucketURL(art, "w320")} alt="" loading="lazy" />}
      <EyeClosed aria-hidden />
    </span>
  );
}

/**
 * The per-item reveal toggle beside a guarded item's Play; nothing is stored.
 * Disabled until the Profile's prefs load, while the guard fails closed.
 */
export function SpoilerRevealButton({
  itemID,
  title,
  state,
}: {
  itemID: string;
  title: string;
  state: SpoilerState;
}) {
  const t = useT();
  // Reads the shared guard without starting a load of its own.
  const loading = useSpoilerGuard(false).guardOn === null;
  if (state !== "hidden" && state !== "revealed") return null;
  const label = t("spoilerRevealItem", { title });
  return (
    <ActionTooltip label={label}>
      <button
        type="button"
        className="bf-row-play bf-spoiler-reveal"
        aria-label={label}
        aria-pressed={state === "revealed"}
        disabled={loading}
        onClick={() => toggleItemReveal(itemID)}
      >
        <ScanEye aria-hidden />
      </button>
    </ActionTooltip>
  );
}

/** Show or hide every unplayed child of a container for the Profile, stored with it. */
export function SpoilerContainerToggle({
  containerID,
  revealed,
}: {
  containerID: string;
  revealed: boolean;
}) {
  const t = useT();
  return (
    <div className="bf-spoiler-bar">
      <button
        type="button"
        className="bf-spoiler-toggle"
        aria-pressed={revealed}
        onClick={() => void setContainerRevealed(containerID, !revealed)}
      >
        <ScanEye aria-hidden />
        {t("spoilerShowContainer")}
      </button>
    </div>
  );
}
