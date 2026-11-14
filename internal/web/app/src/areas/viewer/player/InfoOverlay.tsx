import { ChevronLeft, ChevronRight, Info } from "lucide-react";
import { Fragment, useRef } from "react";
import { MetadataAttributionText } from "@/components/beamfall/MetadataAttributionText";
import { Dialog } from "@/components/ui/astryx-primitives";
import type { MetadataAttribution } from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { ItemActions } from "../actions/ItemActions";
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
  mediaID = "",
  info,
  stream,
  manualRenditionID,
  hlsActive,
  open,
  onOpenChange,
  metadataAttribution,
}: {
  /** The current Media's title; when present it heads the overlay. */
  mediaTitle?: string;
  /** The current Media's id; when present the overlay offers its item actions. */
  mediaID?: string;
  info: PlaybackInfo | null;
  stream: PlaybackStream | null;
  manualRenditionID: string;
  hlsActive: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The plugin-declared credit for this Media's metadata, when it has one. */
  metadataAttribution?: MetadataAttribution;
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
        <Dialog.Trigger className="bf-settings-row" aria-label={t("playbackDetails")}>
          <span aria-hidden="true" className="bf-settings-tile">
            <Info />
          </span>
          <span className="bf-settings-label">{t("playbackDetails")}</span>
          <span className="bf-settings-value" />
          <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog">
            <div className="bf-player-dialog-head">
              <Dialog.Close className="bf-player-dialog-back" aria-label={t("back")}>
                <ChevronLeft aria-hidden="true" />
              </Dialog.Close>
              <Dialog.Title render={<h3 />}>{mediaTitle || t("playbackDetails")}</Dialog.Title>
            </div>
            {mediaID && open && <ItemActions target={{ mediaID }} />}
            {mediaTitle && <p className="bf-up-next-title">{t("playbackDetails")}</p>}
            <dl className="bf-info-rows">
              {rows.map((row) => (
                <Fragment key={row.labelKey}>
                  <dt>{t(row.labelKey as Parameters<typeof t>[0])}</dt>
                  <dd>{row.value}</dd>
                </Fragment>
              ))}
            </dl>
            {metadataAttribution && (
              <p className="bf-info-attribution">
                <MetadataAttributionText attribution={metadataAttribution} />
              </p>
            )}
          </Dialog.Popup>
        </Dialog.Portal>
      </div>
    </Dialog.Root>
  );
}
