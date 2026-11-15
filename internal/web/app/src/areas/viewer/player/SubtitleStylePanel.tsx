import { ChevronLeft, ChevronRight, Type } from "lucide-react";
import { useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { NativeSelect } from "@/components/ui/select";
import { useDisplayLocale, useT } from "@/lib/i18n";
import {
  SUBTITLE_APPEARANCE_FIELDS,
  SUBTITLE_APPEARANCE_OPTIONS,
  SUBTITLE_COLOR_NAMES,
  SUBTITLE_COLORS,
  type SubtitleAppearance,
  type SubtitleAppearanceField,
} from "../lib/subtitleAppearance";
import { formatSync, SUBTITLE_OFFSET_LIMIT } from "./playbackSync";
import { SyncNudger } from "./SyncNudger";

// The subtitle styling/sync panel, ported from renderSubtitleStylePanel
// (app.js): a role="dialog" with one select per appearance field (size,
// background, font, colour, opacity, edge, position; CORE-1681) plus the
// subtitle offset nudger (CORE-1671: ±0.1 s and ±0.5 s steps, the value, a
// reset). The trigger row shows a non-zero offset so it is visible without
// opening the panel. The styling button only surfaces when a subtitle track
// exists; the sync controls disable with an honest "unavailable on this route"
// note when cues are not native TextTracks (an hls.js build rendering its own
// cue DOM). Escape closes and returns focus to the trigger.
type StringKey = Parameters<ReturnType<typeof useT>>[0];

const FIELD_LABELS: Record<SubtitleAppearanceField, StringKey> = {
  size: "subtitleSize",
  background: "subtitleBackground",
  font: "subtitleFont",
  color: "subtitleColor",
  opacity: "subtitleOpacity",
  edge: "subtitleEdge",
  position: "subtitlePosition",
};

// Opacity options are labelled as locale-formatted percentages instead.
const OPTION_LABELS: Record<string, StringKey> = {
  "size:0.85": "subtitleSizeSmall",
  "size:1": "subtitleSizeMedium",
  "size:1.25": "subtitleSizeLarge",
  "size:1.5": "subtitleSizeExtraLarge",
  "background:none": "subtitleBgNone",
  "background:dark": "subtitleBgDark",
  "background:solid": "subtitleBgSolid",
  "font:sans": "subtitleFontSans",
  "font:serif": "subtitleFontSerif",
  "font:mono": "subtitleFontMono",
  "font:casual": "subtitleFontCasual",
  ...Object.fromEntries(
    SUBTITLE_COLORS.map((c) => [`color:${c}`, `subtitleColor${SUBTITLE_COLOR_NAMES[c]}` as const]),
  ),
  "edge:none": "subtitleEdgeNone",
  "edge:outline": "subtitleEdgeOutline",
  "edge:drop-shadow": "subtitleEdgeDropShadow",
  "edge:raised": "subtitleEdgeRaised",
  "edge:depressed": "subtitleEdgeDepressed",
  "position:auto": "subtitlePositionAuto",
  "position:10": "subtitlePositionLow",
  "position:25": "subtitlePositionHigher",
  "position:50": "subtitlePositionMiddle",
  "position:80": "subtitlePositionTop",
};

export function SubtitleStylePanel({
  open,
  onOpenChange,
  appearance,
  onAppearanceChange,
  syncOffset,
  nudgeSync,
  resetSync,
  reset,
  syncSupported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  appearance: SubtitleAppearance;
  onAppearanceChange?: (field: SubtitleAppearanceField, value: string) => void;
  syncOffset: number;
  nudgeSync: (delta: number) => void;
  resetSync: () => void;
  reset: () => void;
  syncSupported: boolean;
}) {
  const t = useT();
  const percent = new Intl.NumberFormat(useDisplayLocale(), { style: "percent" });
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const optionLabel = (field: SubtitleAppearanceField, value: string) =>
    field === "opacity" ? percent.format(Number(value)) : t(OPTION_LABELS[`${field}:${value}`]);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <div ref={anchorRef} className="bf-menu-anchor">
        <Dialog.Trigger className="bf-settings-row">
          <span aria-hidden="true" className="bf-settings-tile">
            <Type />
          </span>
          <span className="bf-settings-label">{t("subtitleStyling")}</span>
          <span className="bf-settings-value">
            {syncOffset !== 0 ? formatSync(syncOffset) : ""}
          </span>
          <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog bf-subtitle-dialog">
            <div className="bf-player-dialog-head">
              <Dialog.Close className="bf-player-dialog-back" aria-label={t("back")}>
                <ChevronLeft aria-hidden="true" />
              </Dialog.Close>
              <Dialog.Title render={<h3 />}>{t("subtitleStyling")}</Dialog.Title>
            </div>

            {SUBTITLE_APPEARANCE_FIELDS.map((field) => (
              <div key={field} className="bf-subtitle-row">
                <label id={`bf-subtitle-${field}-label`} htmlFor={`bf-subtitle-${field}`}>
                  {t(FIELD_LABELS[field])}
                </label>
                <NativeSelect
                  id={`bf-subtitle-${field}`}
                  aria-labelledby={`bf-subtitle-${field}-label`}
                  value={appearance[field]}
                  onValueChange={(value) => onAppearanceChange?.(field, value)}
                >
                  {SUBTITLE_APPEARANCE_OPTIONS[field].map((value) => (
                    <option key={value} value={value}>
                      {optionLabel(field, value)}
                    </option>
                  ))}
                </NativeSelect>
              </div>
            ))}

            <SyncNudger
              label={t("subtitleSync")}
              value={syncOffset}
              min={-SUBTITLE_OFFSET_LIMIT}
              max={SUBTITLE_OFFSET_LIMIT}
              supported={syncSupported}
              onNudge={nudgeSync}
              onReset={resetSync}
            />

            {!syncSupported && (
              <p className="bf-subtitle-unsupported">{t("subtitleSyncUnsupported")}</p>
            )}

            <button type="button" className="bf-subtitle-reset" onClick={reset}>
              {t("reset")}
            </button>
          </Dialog.Popup>
        </Dialog.Portal>
      </div>
    </Dialog.Root>
  );
}
