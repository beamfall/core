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
  Plus,
  Radio,
  ScanEye,
  SquareArrowOutUpRight,
  UserRound,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Menu } from "@/components/ui/astryx-primitives";
import { ActionTooltip } from "@/components/ui/tooltip";
import * as api from "@/lib/api";
import { getExpectedProfile, getIdentityEpochSignal } from "@/lib/api";
import { fetchMedia } from "@/lib/graphql";
import type { ItemTarget, MediaCredit } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { useViewerIdentity } from "@/lib/session";
import { removeFromContinueWatching, useItemActions, useItemHide } from "../actions";
import { entityPath } from "../lib/entityAddress";
import { editServerQueue } from "../lib/musicQueue";
import { moreWithLead } from "../lib/related";
import {
  dispatchAddToQueueToActivePlayer,
  dispatchPlayNextToActivePlayer,
  getActivePlayingMediaID,
  type PlayQueueItem,
  type QueuePlacementAction,
  stagePlayNext,
  useActivePlayingMediaID,
} from "../lib/watchQueue";
import { AddToPlaylistDialog } from "./AddToPlaylistDialog";
import { useStartRadio } from "./StartRadio";

const COPIED_RESET_MS = 2000;

// The card's More menu. A sibling of the card button (never nested inside it),
// built on the Base UI menu so arrow keys, Escape, typeahead and focus return
// come from the primitive. The Profile's watched state loads when the menu
// first opens, so a grid of cards costs no extra requests. A failed load or
// action is reported on the card itself, because the popup has closed by then;
// opening the menu again retries a failed load. Hide takes the item out of
// this Profile's browsing (CORE-2836). Audio Media read "played"
// rather than "watched"; a Media item can be added to a playlist (CORE-1635).
// On the Continue Watching row a Media card can leave the row and keep its
// progress (CORE-2017); the Undo notice reports a failed write.
export type CardLockState = "unlocked" | "locked" | "unknown";

export function MediaItemCardMenu({
  title,
  primaryLabel,
  isMedia,
  isAudio = false,
  mediaClass,
  lockState = "unlocked",
  locked = false,
  expectedProfileID,
  credits,
  containerID,
  href,
  target,
  onPrimary,
  onDetails,
  continueWatching = false,
  onReveal,
}: {
  title: string;
  primaryLabel: string;
  isMedia: boolean;
  /** The Media's class is "audio": the watched entry uses listening wording. */
  isAudio?: boolean;
  /** Reported media class when known (e.g. "audio", "video", "image"). */
  mediaClass?: string;
  /** Lock state when explicitly tracked; unknown or locked hides queue actions (CORE-2845). */
  lockState?: CardLockState;
  locked?: boolean;
  expectedProfileID?: string;
  /** Credit rows already read by the listing, when available. */
  credits?: readonly MediaCredit[];
  containerID?: string;
  /** Absolute link to the item, for Copy link. */
  href: string;
  /** The item the watched entry acts on. */
  target: ItemTarget;
  onPrimary?: () => void;
  /** Opens Media detail without starting playback (CORE-1144, CORE-2034). */
  onDetails?: () => void;
  /** The card is on the Continue Watching row, so it offers to leave it. */
  continueWatching?: boolean;
  /** Reveals a spoiler-guarded item's still and summary (CORE-3057). */
  onReveal?: () => void;
}) {
  const t = useT();
  const identity = useViewerIdentity();
  const activePlayingID = useActivePlayingMediaID();
  const [copied, setCopied] = useState(false);
  const [opened, setOpened] = useState(false);
  const [open, setOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [queuePending, setQueuePending] = useState(false);
  const [queueNotice, setQueueNotice] = useState<{ kind: "status" | "alert"; text: string } | null>(
    null,
  );
  const aliveRef = useRef(true);
  const identityRef = useRef(identity);
  identityRef.current = identity;
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
  const radio = useStartRadio();
  const resetRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const audioID = isAudio && isMedia && "mediaID" in target ? target.mediaID : undefined;
  const effectiveLockState: CardLockState = locked ? "locked" : lockState;
  const canQueue =
    isMedia && "mediaID" in target && mediaClass !== "image" && effectiveLockState === "unlocked";
  const isCurrentPlaying = isMedia && "mediaID" in target && activePlayingID === target.mediaID;
  // Radio starts from an audio Media item, or from an Entity whose card lists audio (CORE-2772).
  const radioSeed = audioID
    ? ({ seedKind: "media", seedID: audioID } as const)
    : isAudio && "entityID" in target
      ? ({ seedKind: "entity", seedID: target.entityID } as const)
      : null;
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
    aliveRef.current = true;
    return () => {
      aliveRef.current = false;
      if (resetRef.current !== null) clearTimeout(resetRef.current);
    };
  }, []);

  useEffect(() => {
    setQueueNotice(null);
  }, [identity]);

  async function queueTrack(placement: QueuePlacementAction) {
    if (!canQueue || !("mediaID" in target) || queuePending || isCurrentPlaying) return;
    const actionIdentity = identity;
    const runEpoch = getIdentityEpochSignal();
    const stillHere = () =>
      aliveRef.current && !runEpoch?.aborted && identityRef.current === actionIdentity;
    setQueuePending(true);
    setQueueNotice(null);
    const nextItem: PlayQueueItem = {
      id: target.mediaID,
      title,
      ...(isAudio ? { mediaClass: "audio" } : {}),
    };
    try {
      const handled =
        placement === "next"
          ? await dispatchPlayNextToActivePlayer(nextItem, actionIdentity)
          : await dispatchAddToQueueToActivePlayer(nextItem, actionIdentity);
      if (!handled) {
        const boundIdentity = Object.hasOwn(api, "getExpectedIdentity")
          ? api.getExpectedIdentity()
          : undefined;
        const targetProfile =
          expectedProfileID ?? getExpectedProfile?.() ?? boundIdentity?.profileID;
        const expectedIdentity =
          boundIdentity && (!targetProfile || targetProfile === boundIdentity.profileID)
            ? boundIdentity
            : undefined;
        await editServerQueue({
          mediaID: target.mediaID,
          placement,
          expectedProfileID: targetProfile,
          expectedIdentity,
          currentPlayingMediaID: getActivePlayingMediaID,
          signal: runEpoch,
        });
        if (placement === "next") stagePlayNext(actionIdentity, nextItem);
      }
      if (stillHere()) {
        setQueueNotice({
          kind: "status",
          text: t(placement === "next" ? "feedsQueuedNext" : "cardQueuedEnd", { title }),
        });
      }
    } catch {
      if (stillHere()) {
        setQueueNotice({ kind: "alert", text: t("musicQueueWriteFailed") });
      }
    } finally {
      if (aliveRef.current) setQueuePending(false);
    }
  }

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
      {queueNotice ? (
        <span
          role={queueNotice.kind}
          className={
            queueNotice.kind === "alert" ? "bf-card-action-error" : "bf-card-action-status"
          }
        >
          {queueNotice.text}
        </span>
      ) : (
        (failed || hide.failed) && (
          <span role="status" className="bf-card-action-error">
            {t("itemActionFailed")}
          </span>
        )
      )}
      <Menu.Root
        onOpenChange={(open) => {
          setOpen(open);
          if (!open) return;
          setQueueNotice(null);
          // A state load that failed is tried again each time the menu opens.
          if (opened && failed && !state) retry();
          setOpened(true);
        }}
      >
        <ActionTooltip label={t("cardMoreActions", { title })} disabled={open}>
          <Menu.Trigger className="bf-card-more" aria-label={t("cardMoreActions", { title })}>
            <MoreVertical aria-hidden="true" size={16} />
          </Menu.Trigger>
        </ActionTooltip>
        <Menu.Portal>
          <Menu.Positioner side="bottom" align="end" sideOffset={6} className="bf-card-menu-layer">
            <Menu.Popup className="bf-card-menu" data-register="viewer">
              {onPrimary && (
                <Menu.Item className="bf-card-menu-item" onClick={onPrimary}>
                  <PrimaryIcon aria-hidden="true" size={15} />
                  {primaryLabel}
                </Menu.Item>
              )}
              {canQueue && (
                <>
                  <Menu.Item
                    className="bf-card-menu-item"
                    disabled={queuePending || isCurrentPlaying}
                    onClick={() => void queueTrack("next")}
                  >
                    <ListPlus aria-hidden="true" size={15} />
                    {t("cardPlayNext")}
                  </Menu.Item>
                  <Menu.Item
                    className="bf-card-menu-item"
                    disabled={queuePending || isCurrentPlaying}
                    onClick={() => void queueTrack("end")}
                  >
                    <Plus aria-hidden="true" size={15} />
                    {t("cardAddToQueue")}
                  </Menu.Item>
                </>
              )}
              {onDetails && (
                <Menu.Item className="bf-card-menu-item" onClick={onDetails}>
                  <SquareArrowOutUpRight aria-hidden="true" size={15} />
                  {t("mediaDetailDetails")}
                </Menu.Item>
              )}
              {onReveal && (
                <Menu.Item className="bf-card-menu-item" onClick={onReveal}>
                  <ScanEye aria-hidden="true" size={15} />
                  {t("spoilerReveal")}
                </Menu.Item>
              )}
              {radioSeed && radio.available && (
                <Menu.Item
                  className="bf-card-menu-item"
                  disabled={radio.pending}
                  onClick={() => void radio.start(radioSeed)}
                >
                  <Radio aria-hidden="true" size={15} />
                  {t("radioStart")}
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
              {continueWatching && isMedia && "mediaID" in target && (
                <Menu.Item
                  className="bf-card-menu-item"
                  onClick={() => void removeFromContinueWatching(target.mediaID, title)}
                >
                  <X aria-hidden="true" size={15} />
                  {t("itemContinueRemove")}
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
      {radio.element}
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
