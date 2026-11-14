import { X } from "lucide-react";
import { Fragment } from "react";
import { Dialog } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import { SHORTCUT_SHEET } from "./playerShortcuts";

// The `?` keyboard shortcut sheet (CORE-1682): a non-modal dialog over the
// stage, portalled into the transport layer so it stays inside the stage in
// fullscreen. Rows come from the same table the key handler resolves against.
// Escape or the close button dismisses it and returns focus to the page.
export function ShortcutHelpOverlay({
  open,
  onOpenChange,
  container,
  skipInterval,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  container: React.RefObject<HTMLElement | null>;
  skipInterval: number;
}) {
  const t = useT();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange} modal={false}>
      <Dialog.Portal container={container}>
        <Dialog.Popup className="bf-player-dialog bf-shortcut-sheet">
          <div className="bf-player-dialog-head">
            <Dialog.Title render={<h3 />}>{t("keyboardShortcuts")}</Dialog.Title>
            <Dialog.Close className="bf-player-dialog-back" aria-label={t("dialogClose")}>
              <X aria-hidden="true" />
            </Dialog.Close>
          </div>
          <dl className="bf-shortcut-rows">
            {SHORTCUT_SHEET.map((row) => (
              <Fragment key={row.label}>
                <dt>
                  {row.keys.map((key, index) => (
                    <Fragment key={key}>
                      {index > 0 && (row.label === "shortcutSeekPercent" ? "–" : " ")}
                      <kbd>{key === "space" ? t("keySpace") : key}</kbd>
                    </Fragment>
                  ))}
                </dt>
                <dd>{t(row.label, { seconds: skipInterval })}</dd>
              </Fragment>
            ))}
          </dl>
        </Dialog.Popup>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
