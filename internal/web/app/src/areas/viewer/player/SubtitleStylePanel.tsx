import { useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { NativeSelect } from "@/components/ui/select";
import { useT } from "@/lib/i18n";
import type { SubtitleBackground } from "./useSubtitleStyling";

// The subtitle styling/sync panel, ported from renderSubtitleStylePanel
// (app.js): a role="dialog" with text-size and background selects plus a sync
// nudger (−0.5s / +0.5s). The styling button only surfaces when a subtitle track
// exists; the sync controls disable with an honest "unavailable on this route"
// note when the active route is hls.js (which renders its own cue DOM). Escape
// closes and returns focus to the trigger.
const SIZE_OPTIONS: { value: number; key: Parameters<ReturnType<typeof useT>>[0] }[] = [
  { value: 0.85, key: "subtitleSizeSmall" },
  { value: 1, key: "subtitleSizeMedium" },
  { value: 1.25, key: "subtitleSizeLarge" },
  { value: 1.5, key: "subtitleSizeExtraLarge" },
];

const BG_OPTIONS: { value: SubtitleBackground; key: Parameters<ReturnType<typeof useT>>[0] }[] = [
  { value: "none", key: "subtitleBgNone" },
  { value: "dark", key: "subtitleBgDark" },
  { value: "solid", key: "subtitleBgSolid" },
];

export function SubtitleStylePanel({
  open,
  onOpenChange,
  sizeScale,
  setSizeScale,
  background,
  setBackground,
  syncOffset,
  nudgeSync,
  reset,
  syncSupported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sizeScale: number;
  setSizeScale: (scale: number) => void;
  background: SubtitleBackground;
  setBackground: (bg: SubtitleBackground) => void;
  syncOffset: number;
  nudgeSync: (delta: number) => void;
  reset: () => void;
  syncSupported: boolean;
}) {
  const t = useT();
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const sign = syncOffset > 0 ? "+" : "";
  const syncDisplay = `${sign}${syncOffset.toFixed(1)}s`;

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <div ref={anchorRef} className="bf-menu-anchor">
        <Dialog.Trigger className="bf-control">{t("subtitleStyling")}</Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog">
            <Dialog.Title render={<h3 />}>{t("subtitleStyling")}</Dialog.Title>

            <div className="bf-subtitle-row">
              <label id="bf-subtitle-size-label" htmlFor="bf-subtitle-size">
                {t("subtitleSize")}
              </label>
              <NativeSelect
                id="bf-subtitle-size"
                aria-labelledby="bf-subtitle-size-label"
                value={String(sizeScale)}
                onValueChange={(value) => setSizeScale(Number(value))}
              >
                {SIZE_OPTIONS.map((option) => (
                  <option key={option.value} value={String(option.value)}>
                    {t(option.key)}
                  </option>
                ))}
              </NativeSelect>
            </div>

            <div className="bf-subtitle-row">
              <label id="bf-subtitle-bg-label" htmlFor="bf-subtitle-bg">
                {t("subtitleBackground")}
              </label>
              <NativeSelect
                id="bf-subtitle-bg"
                aria-labelledby="bf-subtitle-bg-label"
                value={background}
                onValueChange={(value) => setBackground(value as SubtitleBackground)}
              >
                {BG_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {t(option.key)}
                  </option>
                ))}
              </NativeSelect>
            </div>

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
