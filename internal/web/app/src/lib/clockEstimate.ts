// Shared-clock estimation for synchronized playback (CORE-2244). Pure
// functions: a four-timestamp exchange gives one reading of the server clock,
// and the reading with the shortest round trip is the one least bent by the
// network, so it is the estimate.

/**
 * One calibration exchange, all in fractional ms: the local clock when the
 * request left (t1) and when the reply arrived (t4), and the server clock
 * when it received the request (t2) and sent the reply (t3).
 */
export interface ClockExchange {
  t1: number;
  t2: number;
  t3: number;
  t4: number;
}

export interface ClockEstimate {
  /** serverClock − localClock, in fractional ms. */
  offsetMS: number;
  /** The exchange's round trip less the server's own time, in ms. */
  roundTripMS: number;
}

/** An estimate and the local clock reading it was taken at. */
export interface TimedClockEstimate extends ClockEstimate {
  atMS: number;
}

/**
 * The local clock every estimate and every use of an offset reads: wall time
 * with sub-millisecond steps that never jumps while the page lives.
 */
export function localClockMS(): number {
  return performance.timeOrigin + performance.now();
}

/**
 * The clock offset from several exchanges: the one with the shortest round
 * trip decides, so a delayed exchange among them does not move the result.
 * Null when no exchange is usable.
 */
export function estimateClock(exchanges: readonly ClockExchange[]): ClockEstimate | null {
  let best: ClockEstimate | null = null;
  for (const { t1, t2, t3, t4 } of exchanges) {
    const roundTripMS = Math.max(0, t4 - t1 - (t3 - t2));
    const offsetMS = (t2 - t1 + (t3 - t4)) / 2;
    if (!Number.isFinite(roundTripMS) || !Number.isFinite(offsetMS)) continue;
    if (!best || roundTripMS < best.roundTripMS) best = { offsetMS, roundTripMS };
  }
  return best;
}

/** How much error a held estimate may gain per ms of age (100 ppm of clock drift). */
const CLOCK_DRIFT_PER_MS = 0.0001;

/**
 * The estimate to keep after a re-estimate. An offset is wrong by at most half
 * its round trip, plus drift as it ages. The new one replaces the held one
 * when its bound is no worse, or when the two disagree by more than both
 * bounds allow (the clock stepped); otherwise one slow sample would move it.
 */
export function mergeClockEstimate(
  current: TimedClockEstimate | null,
  next: TimedClockEstimate,
): TimedClockEstimate {
  if (!current) return next;
  const age = Math.max(0, next.atMS - current.atMS);
  const currentBound = current.roundTripMS / 2 + age * CLOCK_DRIFT_PER_MS;
  const nextBound = next.roundTripMS / 2;
  if (nextBound <= currentBound) return next;
  if (Math.abs(next.offsetMS - current.offsetMS) > currentBound + nextBound) return next;
  return current;
}
