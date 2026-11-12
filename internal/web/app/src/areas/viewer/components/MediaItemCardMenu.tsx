import {
  Check,
  Circle,
  CircleCheck,
  Link2,
  MoreVertical,
  Play,
  SquareArrowOutUpRight,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Menu } from "@/components/ui/astryx-primitives";
import type { ItemTarget } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useItemActions } from "../actions";

const COPIED_RESET_MS = 2000;

// The card's More menu. A sibling of the card button (never nested inside it),
// built on the Base UI menu so arrow keys, Escape, typeahead and focus return
// come from the primitive. The Profile's watched state loads when the menu
// first opens, so a grid of cards costs no extra requests. A failed load or
// action is reported on the card itself, because the popup has closed by then;
// opening the menu again retries a failed load.
export function MediaItemCardMenu({
  title,
  primaryLabel,
  isMedia,
  href,
  target,
  onPrimary,
}: {
  title: string;
  primaryLabel: string;
  isMedia: boolean;
  /** Absolute link to the item, for Copy link. */
  href: string;
  /** The item the watched entry acts on. */
  target: ItemTarget;
  onPrimary: () => void;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const [opened, setOpened] = useState(false);
  const { state, pending, failed, retry, setWatched } = useItemActions(target, {
    enabled: opened,
  });
  const watched = state?.watched ?? false;
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (resetRef.current !== null) clearTimeout(resetRef.current);
    };
  }, []);

  function copyLink() {
    const clipboard = navigator.clipboard;
    if (!clipboard) return;
    void clipboard.writeText(href).then(
      () => {
        setCopied(true);
        if (resetRef.current !== null) clearTimeout(resetRef.current);
        resetRef.current = setTimeout(() => setCopied(false), COPIED_RESET_MS);
      },
      () => setCopied(false),
    );
  }

  const PrimaryIcon = isMedia ? Play : SquareArrowOutUpRight;

  return (
    <>
      {failed && (
        <span role="status" className="bf-card-action-error">
          {t("itemActionFailed")}
        </span>
      )}
      <Menu.Root
        onOpenChange={(open) => {
          if (!open) return;
          // A state load that failed is tried again each time the menu opens.
          if (opened && failed && !state) retry();
          setOpened(true);
        }}
      >
        <Menu.Trigger className="bf-card-more" aria-label={t("cardMoreActions", { title })}>
          <MoreVertical aria-hidden="true" size={16} />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner side="bottom" align="end" sideOffset={6} className="bf-card-menu-layer">
            <Menu.Popup className="bf-card-menu" data-register="viewer">
              <Menu.Item className="bf-card-menu-item" onClick={onPrimary}>
                <PrimaryIcon aria-hidden="true" size={15} />
                {primaryLabel}
              </Menu.Item>
              <Menu.Item
                className="bf-card-menu-item"
                disabled={pending || !state}
                onClick={() => void setWatched(!watched)}
              >
                {watched ? (
                  <Circle aria-hidden="true" size={15} />
                ) : (
                  <CircleCheck aria-hidden="true" size={15} />
                )}
                {watched ? t("itemMarkUnwatched") : t("itemMarkWatched")}
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
    </>
  );
}
