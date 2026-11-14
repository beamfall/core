import { AudioWaveform, ChevronLeft, ChevronRight } from "lucide-react";
import { useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import { AUDIO_DELAY_LIMIT, formatSync } from "./playbackSync";
import { SyncNudger } from "./SyncNudger";

// The audio sync panel (CORE-1671): a settings row that shows the current audio
// delay and opens a dialog with the delay nudger. The browser can only hold
// sound back (a Web Audio delay line), so the range starts at zero and the panel
// says so; a route whose sound cannot be routed through Web Audio disables the
// nudger with an honest note instead of pretending to apply it.
export function AudioSyncPanel({
  open,
  onOpenChange,
  audioDelay,
  nudgeAudioDelay,
  resetAudioDelay,
  supported,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  audioDelay: number;
  nudgeAudioDelay: (delta: number) => void;
  resetAudioDelay: () => void;
  supported: boolean;
}) {
  const t = useT();
  const anchorRef = useRef<HTMLDivElement | null>(null);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <div ref={anchorRef} className="bf-menu-anchor">
        <Dialog.Trigger className="bf-settings-row">
          <span aria-hidden="true" className="bf-settings-tile">
            <AudioWaveform />
          </span>
          <span className="bf-settings-label">{t("audioSync")}</span>
          <span className="bf-settings-value">
            {audioDelay !== 0 ? formatSync(audioDelay) : ""}
          </span>
          <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog">
            <div className="bf-player-dialog-head">
              <Dialog.Close className="bf-player-dialog-back" aria-label={t("back")}>
                <ChevronLeft aria-hidden="true" />
              </Dialog.Close>
              <Dialog.Title render={<h3 />}>{t("audioSync")}</Dialog.Title>
            </div>

            <SyncNudger
              label={t("audioSync")}
              value={audioDelay}
              min={0}
              max={AUDIO_DELAY_LIMIT}
              supported={supported}
              onNudge={nudgeAudioDelay}
              onReset={resetAudioDelay}
            />

            <p className="bf-subtitle-unsupported">
              {supported ? t("audioSyncDelayOnly") : t("audioSyncUnsupported")}
            </p>
          </Dialog.Popup>
        </Dialog.Portal>
      </div>
    </Dialog.Root>
  );
}
