import { ChevronLeft, ChevronRight, Type } from "lucide-react";
import { useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { NativeSelect } from "@/components/ui/select";
import { useDisplayLocale, useT } from "@/lib/i18n";
import {
  SUBTITLE_APPEARANCE_FIELDS,
  SUBTITLE_APPEARANCE_OPTIONS,
  type SubtitleAppearance,
  type SubtitleAppearanceField,
} from "../lib/subtitleAppearance";

// The subtitle styling/sync panel, ported from renderSubtitleStylePanel
// (app.js): a role="dialog" with one select per appearance field (size,
// background, font, colour, opacity, edge, position; CORE-1681) plus a sync
// nudger (−0.5s / +0.5s). The styling button only surfaces when a subtitle track
// exists; the sync controls disable with an honest "unavailable on this route"
// note when the active route is hls.js. Escape closes and returns focus to the
// trigger.
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
  "color:#ffffff": "subtitleColorWhite",
  "color:#ffff00": "subtitleColorYellow",
  "color:#00ff00": "subtitleColorGreen",
  "color:#00ffff": "subtitleColorCyan",
  "color:#0000ff": "subtitleColorBlue",
  "color:#ff00ff": "subtitleColorMagenta",
  "color:#ff0000": "subtitleColorRed",
  "color:#000000": "subtitleColorBlack",
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
  reset,
  syncSupported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  appearance: SubtitleAppearance;
  onAppearanceChange?: (field: SubtitleAppearanceField, value: string) => void;
  syncOffset: number;
  nudgeSync: (delta: number) => void;
  reset: () => void;
  syncSupported: boolean;
}) {
  const t = useT();
  const percent = new Intl.NumberFormat(useDisplayLocale(), { style: "percent" });
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const sign = syncOffset > 0 ? "+" : "";
  const syncDisplay = `${sign}${syncOffset.toFixed(1)}s`;
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
          <span className="bf-settings-value" />
          <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog">
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

            <div className="bf-subtitle-row">
              <span id="bf-subtitle-sync-label">{t("subtitleSync")}</span>
              <span className="bf-subtitle-sync" aria-labelledby="bf-subtitle-sync-label">
                <button
                  type="button"
                  aria-label={`${t("subtitleSync")} −0.5s`}
                  disabled={!syncSupported}
                  onClick={() => nudgeSync(-0.5)}
                >
                  −
                </button>
                <span className="bf-subtitle-sync-value" aria-live="polite">
                  {syncDisplay}
                </span>
                <button
                  type="button"
                  aria-label={`${t("subtitleSync")} +0.5s`}
                  disabled={!syncSupported}
                  onClick={() => nudgeSync(0.5)}
                >
                  +
                </button>
              </span>
            </div>

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
