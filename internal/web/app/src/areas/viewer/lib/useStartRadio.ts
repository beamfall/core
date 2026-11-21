import { useLayoutEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { ApiError, getIdentityEpochSignal, graphql } from "@/lib/api";
import { useT } from "@/lib/i18n";
import { useSession } from "@/lib/session";
import { mediaPath } from "./mediaAddress";
import { MUSIC_QUEUE_PARAM, type MusicQueueRow, playableMusic } from "./musicQueue";
import { newStartID } from "./watchQueue";

const START_STATION = `mutation StartRadio(
  $seedKind: String!, $seedID: String!, $expectedProfileID: String!, $confirmReplace: Boolean!
) {
  stationStart(seedKind: $seedKind, seedID: $seedID, expectedProfileID: $expectedProfileID,
    confirmReplace: $confirmReplace) { id title locked mediaClass }
}`;

export type RadioSeed = "track" | "artist" | "album";

export function audioEntitySeed(archetype: string | undefined): RadioSeed | undefined {
  if (archetype === "person") return "artist";
  if (archetype === "container") return "album";
}

export function useStartRadio(seedKind: RadioSeed | undefined, seedID: string) {
  const t = useT();
  const navigate = useNavigate();
  const { session, revealed } = useSession();
  const profileID = session?.profileID;
  const epoch = getIdentityEpochSignal();
  const pending = useRef<AbortController | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<"radioEmpty" | "radioFailed">();

  // A completion for an old page, Profile or Reveal session cannot navigate.
  useLayoutEffect(() => {
    setBusy(false);
    setNotice(undefined);
    return () => {
      pending.current?.abort();
      pending.current = null;
    };
  }, [seedKind, seedID, profileID, revealed, epoch]);

  async function start() {
    if (!seedKind || !seedID || !profileID || pending.current || epoch?.aborted) return;
    const controller = new AbortController();
    pending.current = controller;
    const signal = epoch ? AbortSignal.any([epoch, controller.signal]) : controller.signal;
    setBusy(true);
    setNotice(undefined);
    const send = (confirmReplace: boolean) =>
      graphql<{ stationStart: MusicQueueRow[] }>(
        START_STATION,
        { seedKind, seedID, expectedProfileID: profileID, confirmReplace },
        signal,
        { adminUnlock: false },
      );
    try {
      let result: Awaited<ReturnType<typeof send>>;
      try {
        result = await send(false);
      } catch (error) {
        if (
          !(error instanceof ApiError) ||
          !Array.isArray(error.payload) ||
          !error.payload.some((entry) => entry?.extensions?.reason === "QUEUE_REPLACE_CONFIRMATION")
        ) {
          throw error;
        }
        if (signal.aborted || !window.confirm(t("radioReplaceQueue"))) return;
        result = await send(true);
      }
      if (signal.aborted) return;
      const first = result.stationStart.find(playableMusic);
      if (!first) {
        setNotice("radioEmpty");
        return;
      }
      const search = new URLSearchParams({ [MUSIC_QUEUE_PARAM]: profileID, t: "0" });
      navigate(`${mediaPath("watch", first.id)}?${search}`, {
        state: { startID: newStartID() },
      });
    } catch {
      if (!signal.aborted) setNotice("radioFailed");
    } finally {
      if (pending.current === controller) {
        pending.current = null;
        setBusy(false);
      }
    }
  }

  return seedKind && seedID && profileID ? { start, busy, notice } : undefined;
}
