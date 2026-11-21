import { ListMusic, X } from "lucide-react";
import { useRef } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { playableMusic, useMusicQueue } from "../lib/musicQueue";

export function MusicQueuePanel({
  open,
  onOpenChange,
  onPlay,
  currentID,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onPlay: (id: string) => void;
  currentID: string;
}) {
  const t = useT();
  const anchorRef = useRef<HTMLDivElement | null>(null);
  const { rows, settled, refresh, isCurrent } = useMusicQueue(open);
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
            {!settled ? (
              <p role="status">{t("loading")}</p>
            ) : !rows ? (
              <>
                <p role="alert">{t("musicQueueReadFailed")}</p>
                <Button size="sm" onClick={() => void refresh()}>
                  {t("tryAgain")}
                </Button>
              </>
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
                  <Button
                    size="sm"
                    onClick={async () => {
                      const next = (await refresh())?.find(playableMusic);
                      if (next && isCurrent()) {
                        onOpenChange(false);
                        onPlay(next.id);
                      }
                    }}
                  >
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
