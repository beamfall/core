import { ListMusic, X } from "lucide-react";
import { useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { type MusicQueueView, playableMusic } from "../lib/musicQueue";

export function MusicQueuePanel({
  open,
  onRetry,
  onOpenChange,
  onPlay,
  currentID,
  queue,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPlay: () => void;
  onRetry: () => void;
  currentID: string;
  queue: MusicQueueView | undefined;
}) {
  const t = useT();
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const { rows, failed, invalid } = queue ?? {};
  const first = rows?.find(playableMusic);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <div ref={anchorRef} className="relative">
        <Dialog.Trigger aria-label={t("musicQueueTitle")} className="bf-control is-icon">
          <ListMusic aria-hidden />
        </Dialog.Trigger>
        <Dialog.Portal container={anchorRef}>
          <Dialog.Popup className="bf-player-dialog">
            <div className="bf-player-dialog-head">
              <Dialog.Close className="bf-player-dialog-back" aria-label={t("dialogClose")}>
                <X aria-hidden />
              </Dialog.Close>
              <Dialog.Title render={<h3 />}>{t("musicQueueTitle")}</Dialog.Title>
            </div>
            {invalid ? (
              <p role="alert">{t("playerTerminalUnavailable")}</p>
            ) : failed ? (
              <>
                <p role="alert">{t("musicQueueReadFailed")}</p>
                <Button size="sm" onClick={onRetry}>
                  {t("tryAgain")}
                </Button>
              </>
            ) : !rows ? (
              <p role="status">{t("loading")}</p>
            ) : (
              <>
                {rows.length > 0 && (
                  <ol className="m-0 grid max-h-[min(45vh,20rem)] list-inside list-decimal gap-2 overflow-auto p-0">
                    {rows.map((row) => (
                      <li key={row.id} aria-current={row.id === currentID ? "true" : undefined}>
                        {row.locked ? t("musicQueueLockedItem") : row.title}
                      </li>
                    ))}
                  </ol>
                )}
                {first ? (
                  <Button size="sm" onClick={onPlay}>
                    {t("musicQueuePlay")}
                  </Button>
                ) : (
                  <p role="status">{t("musicQueueEmpty")}</p>
                )}
              </>
            )}
          </Dialog.Popup>
        </Dialog.Portal>
      </div>
    </Dialog.Root>
  );
}
