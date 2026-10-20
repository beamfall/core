import { Fragment, useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import type { PlaybackInfo, PlaybackStream } from "../lib/playback";
import { buildRouteDetailModel } from "../lib/transport";

// The info drill-in overlay, ported from renderInfoOverlay / togglePlayerDialog
// (app.js): a role="dialog" panel of honest raw route detail (route, engine,
// quality/bitrate, transcode reason). Codec/container are not on the web wire,
// so only server-reported fields appear — the model never invents them. The
// dialog holds plain content, so Tab moves naturally; Escape closes and returns
// focus to the trigger (the a11y gate, matching the vanilla dialog pattern).
export function InfoOverlay({
  mediaTitle = "",
  info,
  stream,
  manualRenditionID,
  hlsActive,
  open,
  onOpenChange,
}: {
  /** The current Media's title; when present it heads the overlay. */
  mediaTitle?: string;
  info: PlaybackInfo | null;
  stream: PlaybackStream | null;
  manualRenditionID: string;
  hlsActive: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const rows = buildRouteDetailModel({
    stream,
    info,
    manualRenditionID,
    hlsActive,
    labels: {
      directPlay: t("directPlay"),
      directStream: t("directStream"),
      serverTranscode: t("serverTranscode"),
      auto: t("auto"),
      upTo: t("upTo"),
      engineNative: t("engineNative"),
      engineNativeHls: t("engineNativeHls"),
      engineHlsJs: t("engineHlsJs"),
      reasonRequiresTranscode: t("reasonRequiresTranscode"),
    },
  });

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <div ref={anchorRef} className="bf-menu-anchor">
        <Dialog.Trigger className="bf-control" aria-label={t("playbackDetails")}>
          {t("info")}
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog">
            <Dialog.Title render={<h3 />}>{mediaTitle || t("playbackDetails")}</Dialog.Title>
            {mediaTitle && <p className="bf-up-next-title">{t("playbackDetails")}</p>}
            <dl className="bf-info-rows">
              {rows.map((row) => (
                <Fragment key={row.labelKey}>
                  <dt>{t(row.labelKey as Parameters<typeof t>[0])}</dt>
                  <dd>{row.value}</dd>
                </Fragment>
              ))}
            </dl>
          </Dialog.Popup>
        </Dialog.Portal>
      </div>
    </Dialog.Root>
  );
}
