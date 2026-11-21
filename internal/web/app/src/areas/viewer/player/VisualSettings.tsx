import { Sparkles } from "lucide-react";
import { NativeSelect } from "@/components/ui/select";
import { useT } from "@/lib/i18n";
import { canVisualize, setVisualChoice, useVisualChoice } from "./visualizer/visualChoice";
import { VISUALS } from "./visualizer/visuals";

/**
 * The visualizer row of the player settings sheet (CORE-2368): which Oscillux visual the audio
 * player shows. "Rotate" moves through them all, "One per album" keeps one per album, and each
 * visual can be pinned by its Oscillux catalog name; "Off" shows none (CORE-2814). The choice
 * belongs to this browser.
 */
export function VisualSettings() {
  const t = useT();
  const choice = useVisualChoice();
  // Without WebGL2 the visualizer slot stays empty, so there is nothing to choose.
  if (!canVisualize()) return null;
  return (
    <label className="bf-settings-row">
      <span aria-hidden="true" className="bf-settings-tile">
        <Sparkles />
      </span>
      <span className="bf-settings-label">{t("visualChoice")}</span>
      <NativeSelect
        aria-label={t("visualChoice")}
        className="bf-settings-select"
        value={choice}
        onValueChange={setVisualChoice}
      >
        <option value="rotate">{t("visualChoiceRotate")}</option>
        <option value="album">{t("visualChoiceAlbum")}</option>
        <option value="off">{t("visualChoiceOff")}</option>
        {VISUALS.map((visual) => (
          <option key={visual.id} value={visual.id}>
            {visual.name}
          </option>
        ))}
      </NativeSelect>
    </label>
  );
}
