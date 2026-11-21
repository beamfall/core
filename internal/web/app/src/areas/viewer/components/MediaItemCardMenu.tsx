import {
  Check,
  Circle,
  CircleCheck,
  Disc3,
  Eye,
  EyeOff,
  Link2,
  ListPlus,
  MoreVertical,
  Play,
  SquareArrowOutUpRight,
  UserRound,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Menu } from "@/components/ui/astryx-primitives";
import { fetchMedia } from "@/lib/graphql";
import type { ItemTarget, MediaCredit } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { useItemActions, useItemHide } from "../actions";
import { entityPath } from "../lib/entityAddress";
import { moreWithLead } from "../lib/related";
import { AddToPlaylistDialog } from "./AddToPlaylistDialog";

const COPIED_RESET_MS = 2000;

// The card's More menu. A sibling of the card button (never nested inside it),
// built on the Base UI menu so arrow keys, Escape, typeahead and focus return
// come from the primitive. The Profile's watched state loads when the menu
// first opens, so a grid of cards costs no extra requests. A failed load or
// action is reported on the card itself, because the popup has closed by then;
// opening the menu again retries a failed load. Hide takes the item out of
// this Profile's browsing (CORE-2836). Audio Media read "played"
// rather than "watched"; a Media item can be added to a playlist (CORE-1635).
export function MediaItemCardMenu({
  title,
  primaryLabel,
  isMedia,
  isAudio = false,
  credits,
  containerID,
  href,
  target,
  onPrimary,
  onDetails,
}: {
  title: string;
  primaryLabel: string;
  isMedia: boolean;
  /** The Media's class is "audio": the watched entry uses listening wording. */
  isAudio?: boolean;
  /** Credit rows already read by the listing, when available. */
  credits?: readonly MediaCredit[];
  containerID?: string;
  /** Absolute link to the item, for Copy link. */
  href: string;
  /** The item the watched entry acts on. */
  target: ItemTarget;
  onPrimary: () => void;
  /** Opens Media detail without starting playback (CORE-1144, CORE-2034). */
  onDetails?: () => void;
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const [copied, setCopied] = useState(false);
  const [opened, setOpened] = useState(false);
  const [open, setOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [creditRead, setCreditRead] = useState<{
    identity: string;
    mediaID: string;
    credits: readonly MediaCredit[];
  } | null>(null);
  const { state, pending, failed, retry, setWatched } = useItemActions(target, {
    enabled: opened,
  });
  const watched = state?.watched ?? false;
  const hide = useItemHide(target, title, { enabled: opened });
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const audioID = isAudio && isMedia && "mediaID" in target ? target.mediaID : undefined;
  const loadedCredits =
    creditRead && creditRead.identity === identity && creditRead.mediaID === audioID
      ? creditRead.credits
      : undefined;
  const audioCredits = audioID && identity ? (credits ?? loadedCredits) : undefined;
  const artistID = moreWithLead(audioCredits)?.entityID;
  const albumCredit = audioCredits?.find(
    (credit) => credit.archetype === "container" && credit.entityID,
  );
  const albumID = audioID && identity ? (albumCredit?.entityID ?? containerID) : undefined;

  useEffect(() => {
    setCreditRead((read) =>
      read?.identity === identity && read?.mediaID === audioID ? read : null,
    );
  }, [audioID, identity]);

  useEffect(() => {
    if (!open || !audioID || !identity || audioCredits) return;
    const controller = new AbortController();
    fetchMedia(audioID, controller.signal)
      .then((media) => {
        if (!controller.signal.aborted) {
          setCreditRead({ identity, mediaID: audioID, credits: media?.credits ?? [] });
        }
      })
      .catch(() => {
        // A later opening can retry the optional credit read.
      });
    return () => controller.abort();
  }, [open, audioID, identity, audioCredits]);

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
  const watchedLabel = isAudio
    ? t(watched ? "itemMarkUnplayed" : "itemMarkPlayed")
    : t(watched ? "itemMarkUnwatched" : "itemMarkWatched");

  return (
    <>
      {(failed || hide.failed) && (
        <span role="status" className="bf-card-action-error">
          {t("itemActionFailed")}
        </span>
      )}
      <Menu.Root
        onOpenChange={(open) => {
          setOpen(open);
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
              {onDetails && (
                <Menu.Item className="bf-card-menu-item" onClick={onDetails}>
                  <SquareArrowOutUpRight aria-hidden="true" size={15} />
                  {t("mediaDetailDetails")}
                </Menu.Item>
              )}
              {artistID && (
                <Menu.LinkItem
                  className="bf-card-menu-item"
                  render={<Link to={entityPath(artistID)} />}
                >
                  <UserRound aria-hidden="true" size={15} />
                  {t("trackGoToArtist")}
                </Menu.LinkItem>
              )}
              {albumID && (
                <Menu.LinkItem
                  className="bf-card-menu-item"
                  render={<Link to={entityPath(albumID)} />}
                >
                  <Disc3 aria-hidden="true" size={15} />
                  {t("trackGoToAlbum")}
                </Menu.LinkItem>
              )}
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
                {watchedLabel}
              </Menu.Item>
              {isMedia && "mediaID" in target && (
                <Menu.Item className="bf-card-menu-item" onClick={() => setAddOpen(true)}>
                  <ListPlus aria-hidden="true" size={15} />
                  {t("addToPlaylist")}
                </Menu.Item>
              )}
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
      {addOpen && "mediaID" in target && (
        <AddToPlaylistDialog
          mediaID={target.mediaID}
          title={title}
          onClose={() => setAddOpen(false)}
        />
      )}
    </>
  );
}
