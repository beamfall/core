import { useEffect, useState } from "react";
import { fetchMediaRows } from "@/lib/graphql/browse";
import type { GenericMediaInfo, SourceItem } from "@/lib/graphql/types";
import { useContainerContext, useSequenceNext } from "../lib/containerContext";
import {
  creditAttribute,
  SEQUENCE_ATTRIBUTE,
  type SequencePosition,
  sequencePosition,
} from "../lib/relationOrder";

/** The next item offered once a sequenced item ends: never queued, never started. */
export interface SequenceNext {
  /** The container that sequences both items. */
  container: SequencePosition;
  item: SourceItem;
  /** The next item's own sequence in that container, "" until read or when absent. */
  sequence: string;
}

/**
 * The player's finish state for an audio Media its credit sequences in a
 * container (CORE-3013): once playback ends, the next item in that sequence,
 * from the same container context the detail page reads. It is only an offer
 * (ADR-0055 §2.4): nothing here queues or plays it. Playing or seeking again
 * clears the finish state; an item with no next shows the plain ended state.
 *
 * `enabled` is false while some queue advances past the end, so the offer
 * never flashes over an advance.
 */
export function useSequenceFinish({
  video,
  media,
  mediaID,
  enabled,
}: {
  video: HTMLVideoElement | null;
  media: GenericMediaInfo | null;
  mediaID: string;
  enabled: boolean;
}): SequenceNext | null {
  const position = sequencePosition(media);
  // Only a sequenced item reads its container, so other audio costs nothing.
  const context = useContainerContext(position ? media : null, mediaID);
  const next = useSequenceNext(position, mediaID, context);
  const [endedID, setEndedID] = useState<string | null>(null);
  const ended = enabled && endedID === mediaID;

  useEffect(() => {
    if (!video || !enabled) return;
    const onEnded = () => setEndedID(mediaID);
    const onResume = () => setEndedID(null);
    // Bind to the element's current state: it may have ended, or played again,
    // while these listeners were off (docked, or under a queue).
    if (video.ended) onEnded();
    else onResume();
    video.addEventListener("ended", onEnded);
    video.addEventListener("play", onResume);
    video.addEventListener("seeking", onResume);
    return () => {
      video.removeEventListener("ended", onEnded);
      video.removeEventListener("play", onResume);
      video.removeEventListener("seeking", onResume);
    };
  }, [video, enabled, mediaID]);

  // The next item's own place, read once it is offered.
  const nextID = ended ? (next?.id ?? "") : "";
  const containerID = position?.id ?? "";
  const [sequence, setSequence] = useState<{ key: string; value: string } | null>(null);
  const key = `${containerID}\n${nextID}`;
  useEffect(() => {
    if (!nextID) return;
    const controller = new AbortController();
    fetchMediaRows([nextID], controller.signal)
      .then((rows) => {
        if (controller.signal.aborted) return;
        const value = creditAttribute(rows[nextID]?.credits, containerID, SEQUENCE_ATTRIBUTE);
        setSequence({ key, value });
      })
      .catch(() => {
        // The offer stands without its position.
      });
    return () => controller.abort();
  }, [key, nextID, containerID]);

  if (!ended || !position || !next) return null;
  return { container: position, item: next, sequence: sequence?.key === key ? sequence.value : "" };
}
