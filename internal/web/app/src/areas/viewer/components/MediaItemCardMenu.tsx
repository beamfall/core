import { Check, Link2, MoreVertical, Play, SquareArrowOutUpRight } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Menu } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";

const COPIED_RESET_MS = 2000;

// The card's More menu. A sibling of the card button (never nested inside it),
// built on the Base UI menu so arrow keys, Escape, typeahead and focus return
// come from the primitive. Entries are limited to what the server supports
// today; state-changing entries arrive with their mutations.
export function MediaItemCardMenu({
  title,
  primaryLabel,
  isMedia,
  href,
  onPrimary,
}: {
  title: string;
  primaryLabel: string;
  isMedia: boolean;
  /** Absolute link to the item, for Copy link. */
  href: string;
  onPrimary: () => void;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);
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
    <Menu.Root>
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
  );
}
