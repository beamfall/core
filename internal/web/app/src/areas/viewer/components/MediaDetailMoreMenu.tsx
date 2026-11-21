import { Check, Eye, EyeOff, Link2, ListPlus, MoreHorizontal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Menu } from "@/components/ui/astryx-primitives";
import { buttonVariants } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { useItemHide } from "../actions";
import { mediaPath } from "../lib/mediaAddress";
import { AddToPlaylistDialog } from "./AddToPlaylistDialog";

const COPIED_RESET_MS = 2000;

// The More menu on a Media detail page (CORE-2436): the actions that do not
// earn a button of their own beside Play. Built on the Base UI menu like the
// card menu, so arrow keys, Escape and focus return come from the primitive.
// Add to playlist opens the existing manager-gated dialog (CORE-1635); Hide
// takes the item out of this Profile's browsing (CORE-2836).
export function MediaDetailMoreMenu({ mediaID, title }: { mediaID: string; title: string }) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [opened, setOpened] = useState(false);
  const hide = useItemHide({ mediaID }, title, { enabled: opened });
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (resetRef.current !== null) clearTimeout(resetRef.current);
    };
  }, []);

  function copyLink() {
    const clipboard = navigator.clipboard;
    if (!clipboard) return;
    const path = mediaPath("media", mediaID);
    const href = new URL(`${import.meta.env.BASE_URL}${path.slice(1)}`, window.location.origin)
      .href;
    void clipboard.writeText(href).then(
      () => {
        setCopied(true);
        if (resetRef.current !== null) clearTimeout(resetRef.current);
        resetRef.current = setTimeout(() => setCopied(false), COPIED_RESET_MS);
      },
      () => setCopied(false),
    );
  }

  return (
    <>
      {hide.failed && (
        <span role="status" className="bf-card-action-error">
          {t("itemActionFailed")}
        </span>
      )}
      <Menu.Root onOpenChange={(open) => open && setOpened(true)}>
        <Menu.Trigger
          className={`${buttonVariants({ variant: "ghost", size: "lg" })} bf-detail-secondary bf-detail-more`}
          aria-label={t("cardMoreActions", { title })}
        >
          <MoreHorizontal aria-hidden />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner side="bottom" align="end" sideOffset={6} className="bf-card-menu-layer">
            <Menu.Popup className="bf-card-menu" data-register="viewer">
              <Menu.Item className="bf-card-menu-item" onClick={() => setAddOpen(true)}>
                <ListPlus aria-hidden="true" size={15} />
                {t("addToPlaylist")}
              </Menu.Item>
              <Menu.Item
                className="bf-card-menu-item"
                disabled={hide.pending || hide.hidden === null}
                onClick={() => void hide.toggle()}
              >
                {hide.hidden ? (
                  <Eye aria-hidden="true" size={15} />
                ) : (
                  <EyeOff aria-hidden="true" size={15} />
                )}
                {t(hide.hidden ? "itemUnhide" : "itemHide")}
              </Menu.Item>
              <Menu.Item className="bf-card-menu-item" closeOnClick={false} onClick={copyLink}>
                {copied ? (
                  <Check aria-hidden="true" size={15} />
                ) : (
                  <Link2 aria-hidden="true" size={15} />
                )}
                <span aria-live="polite">{copied ? t("cardLinkCopied") : t("cardCopyLink")}</span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
      {addOpen && (
        <AddToPlaylistDialog mediaID={mediaID} title={title} onClose={() => setAddOpen(false)} />
      )}
    </>
  );
}
