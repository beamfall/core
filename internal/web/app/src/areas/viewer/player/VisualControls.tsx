import { ChevronLeft, ChevronRight, Sparkles } from "lucide-react";
import { useT } from "@/lib/i18n";
import {
  canVisualize,
  stepVisual,
  toggleVisualizer,
  useShownVisual,
  useVisualChoice,
} from "./visualizer/visualChoice";
import { visualById } from "./visualizer/visuals";

/**
 * The audio transport's visualizer control (CORE-2814): previous visual, an on/off toggle that
 * names the visual on screen, and next visual. Stepping pins a visual, the same choice the
 * settings sheet makes; V and Shift+V do the same from the keyboard.
 */
export function VisualControls() {
  const t = useT();
  const choice = useVisualChoice();
  const shown = useShownVisual();
  // Without WebGL2 the visualizer slot stays empty, so there is nothing to control.
  if (!canVisualize()) return null;
  const off = choice === "off";
  const name = off
    ? t("visualChoiceOff")
    : (visualById(shown ?? "")?.name ??
      visualById(choice)?.name ??
      (choice === "album" ? t("visualChoiceAlbum") : t("visualChoiceRotate")));
  return (
    <div role="group" aria-label={t("visualChoice")} className="bf-visual-controls">
      <button
        type="button"
        onClick={() => stepVisual(-1)}
        aria-label={t("visualPrevious")}
        className="bf-control is-icon"
      >
        <ChevronLeft aria-hidden="true" />
      </button>
      <button
        type="button"
        onClick={toggleVisualizer}
        aria-pressed={!off}
        aria-label={t("visualCurrent", { name })}
        className="bf-control bf-visual-toggle"
      >
        <Sparkles aria-hidden="true" />
        <span className="bf-visual-name">{name}</span>
      </button>
      <button
        type="button"
        onClick={() => stepVisual(1)}
        aria-label={t("visualNext")}
        className="bf-control is-icon"
      >
        <ChevronRight aria-hidden="true" />
      </button>
    </div>
  );
}
