import { useCallback, useEffect, useRef, useState } from "react";
import { localClockMS } from "@/lib/clockEstimate";
import {
  calibrateWatchPartyClock,
  fetchPartySession,
  fetchPlaybackAdjustmentRanges,
  type PartySession,
  type PlaybackAdjustmentRanges,
} from "@/lib/graphql";
import { useT } from "@/lib/i18n";
import { POLL_MS } from "./partySync";
import { useVisiblePoll } from "./useVisiblePoll";

export type PartyNotice = { tone: "ok" | "error"; text: string };

/** Runs one host operation and applies the session it returns. */
export type RunPartyOp = (
  op: () => Promise<PartySession | null>,
  success?: string,
) => Promise<boolean>;

export interface PartyHost {
  /** undefined while the first read is out; null when there is no live session. */
  session: PartySession | null | undefined;
  loadError: boolean;
  notice: PartyNotice | null;
  ranges: PlaybackAdjustmentRanges | null;
  serverNow: () => number;
  run: RunPartyOp;
}

/**
 * The host's live party session (CORE-2945), polled while the page is
 * visible, and the one way to change it. Shared by the party host page and
 * the karaoke Library's party rail (CORE-3362).
 */
export function usePartyHost(): PartyHost {
  const t = useT();
  const [session, setSession] = useState<PartySession | null | undefined>(undefined);
  const [loadError, setLoadError] = useState(false);
  const [notice, setNotice] = useState<PartyNotice | null>(null);
  const [ranges, setRanges] = useState<PlaybackAdjustmentRanges | null>(null);
  // serverClock − localClock; 0 until calibrated (or when calibration is refused).
  const clockOffsetRef = useRef(0);
  // Requests are numbered as they start, so a poll that set out before a
  // mutation never overwrites the newer session the mutation returned.
  const seqRef = useRef(0);
  const appliedRef = useRef(0);

  const apply = useCallback((seq: number, next: PartySession | null) => {
    if (seq < appliedRef.current) return;
    appliedRef.current = seq;
    setSession(next);
  }, []);

  useVisiblePoll(async (signal) => {
    const seq = ++seqRef.current;
    try {
      const next = await fetchPartySession(signal);
      apply(seq, next);
      setLoadError(false);
    } catch (error) {
      if (!signal.aborted) setLoadError(true);
      throw error;
    }
  }, POLL_MS);

  useEffect(() => {
    const controller = new AbortController();
    fetchPlaybackAdjustmentRanges(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setRanges(next);
      })
      .catch(() => {});
    calibrateWatchPartyClock()
      .then((estimate) => {
        clockOffsetRef.current = estimate.offsetMS;
      })
      .catch(() => {});
    return () => controller.abort();
  }, []);

  const serverNow = useCallback(() => localClockMS() + clockOffsetRef.current, []);

  const run = useCallback<RunPartyOp>(
    async (op, success) => {
      const seq = ++seqRef.current;
      try {
        const next = await op();
        apply(seq, next);
        setNotice(success ? { tone: "ok", text: success } : null);
        return true;
      } catch {
        setNotice({ tone: "error", text: t("partyHostActionError") });
        return false;
      }
    },
    [apply, t],
  );

  return { session, loadError, notice, ranges, serverNow, run };
}
