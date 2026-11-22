import { useSyncExternalStore } from "react";
import { getIdentityEpochSignal, graphql } from "@/lib/api";

// Radio (CORE-2772): a Station started from one Media or one Entity fills the
// Profile's Play Queue with related Media and keeps topping it up while it
// plays. The server's Station generator picks the Media and says why each was
// picked; this module starts a Station, remembers it for the Profile that
// started it, and adds more when the queue runs low. The queue itself stays the
// server's Play Queue, so the player walks it the usual way.
//
// The Station lives in memory only and belongs to the identity epoch that
// started it: a relock, Reveal change, Profile switch or reload drops it, so
// no title or reason outlives the access that produced it.

/** What a Station starts from: one Media item, or one Entity (a person, container or category). */
export type RadioSeedKind = "media" | "entity";

/** Hard Station rules: only Media from these decades, only Media credited to one of these Entities. */
export interface RadioRules {
  decades?: number[];
  anyEntityIDs?: string[];
}

interface ServerRules extends RadioRules {
  excludeQueued?: boolean;
}

export interface RadioStation {
  profileID: string;
  seedKind: RadioSeedKind;
  seedID: string;
  rules: RadioRules;
  /** Titles of the Entities the rules name, for the player's tuning. */
  ruleTitles?: Record<string, string>;
  /** The server's reason for each Media it queued, by Media ID. */
  reasons: Record<string, string>;
}

interface Candidate {
  mediaID: string;
  title: string;
  explanation: string;
}

const GENERATE = `query RadioGenerate($seedKind: String!, $seedID: String!, $seed: String, $rulesJSON: String) {
  stationGenerate(seedKind: $seedKind, seedID: $seedID, seed: $seed, rulesJSON: $rulesJSON) {
    candidates { mediaID title explanation }
    explanation
  }
}`;
const START = `mutation RadioStart($seedKind: String!, $seedID: String!, $seed: String, $rulesJSON: String, $confirmReplace: Boolean, $expectedProfileID: String) {
  stationStart(seedKind: $seedKind, seedID: $seedID, seed: $seed, rulesJSON: $rulesJSON, confirmReplace: $confirmReplace, expectedProfileID: $expectedProfileID) { id }
}`;
const ADD = `mutation RadioAdd($mediaID: String!, $expectedProfileID: String) {
  queueAdd(mediaID: $mediaID, expectedProfileID: $expectedProfileID) { id }
}`;

/** At most this many Media are added per top-up, so one top-up stays a few requests. */
export const RADIO_TOP_UP = 10;
/** A top-up starts when this few playable items remain after the current one. */
export const RADIO_LOW_WATER = 5;

function randomSeed(): string {
  return Math.random().toString(36).slice(2);
}

function rulesJSON(rules: ServerRules): string {
  const cleaned: ServerRules = {};
  if (rules.decades?.length) cleaned.decades = rules.decades;
  if (rules.anyEntityIDs?.length) cleaned.anyEntityIDs = rules.anyEntityIDs;
  if (rules.excludeQueued) cleaned.excludeQueued = true;
  return JSON.stringify(cleaned);
}

type StationSeed = Pick<RadioStation, "seedKind" | "seedID"> & { rules: ServerRules };

async function generate(station: StationSeed, seed: string) {
  const data = await graphql<{ stationGenerate: { candidates: Candidate[]; explanation: string } }>(
    GENERATE,
    {
      seedKind: station.seedKind,
      seedID: station.seedID,
      seed,
      rulesJSON: rulesJSON(station.rules),
    },
  );
  return data.stationGenerate;
}

// --- The active Station, one per browser tab, in memory only ---------------

interface Active {
  station: RadioStation;
  epoch: AbortSignal | undefined;
}

let active: Active | null = null;
// Bumped on every start, stop and epoch end; a top-up that sees it change stops
// before its next queue write.
let generation = 0;
const listeners = new Set<() => void>();

function notify() {
  for (const listener of listeners) listener();
}

function setActive(next: RadioStation | null) {
  generation++;
  if (!next) {
    active = null;
  } else {
    const epoch = getIdentityEpochSignal();
    const mine = generation;
    active = { station: next, epoch };
    // The identity that started the Station ended: forget it, fail closed.
    epoch?.addEventListener("abort", () => generation === mine && setActive(null), {
      once: true,
    });
  }
  notify();
}

function current(): RadioStation | null {
  if (!active) return null;
  const epoch = getIdentityEpochSignal();
  if (active.epoch !== epoch || epoch?.aborted) return null;
  return active.station;
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The Station playing for this Profile, or null. */
export function activeRadio(profileID: string | null | undefined): RadioStation | null {
  const station = current();
  return profileID && station?.profileID === profileID ? station : null;
}

export function useActiveRadio(profileID: string | null | undefined): RadioStation | null {
  const station = useSyncExternalStore(subscribe, current, () => null);
  return profileID && station?.profileID === profileID ? station : null;
}

/** Ends the Station: the queue keeps what it holds, and nothing more is added. */
export function stopRadio() {
  setActive(null);
}

// --- Start and top up -------------------------------------------------------

export type RadioStartResult =
  | { status: "started"; firstMediaID: string; station: RadioStation }
  | { status: "needs-confirm" }
  | { status: "empty"; message: string };

/**
 * Starts a Station into the Profile's Play Queue. A queue that already holds
 * the listener's own items is replaced only with confirmReplace; without it
 * the result asks for confirmation and nothing changes.
 */
export async function startRadio(input: {
  profileID: string;
  seedKind: RadioSeedKind;
  seedID: string;
  rules?: RadioRules;
  ruleTitles?: Record<string, string>;
  confirmReplace?: boolean;
}): Promise<RadioStartResult> {
  const station = { seedKind: input.seedKind, seedID: input.seedID, rules: input.rules ?? {} };
  // The same seed makes the preview and the started window identical, so the
  // preview's reasons describe what was queued; the server's reply says which.
  const seed = randomSeed();
  const preview = await generate(station, seed);
  if (!preview.candidates.length) return { status: "empty", message: preview.explanation };
  let queued: { id: string }[];
  try {
    const data = await graphql<{ stationStart: { id: string }[] }>(START, {
      seedKind: station.seedKind,
      seedID: station.seedID,
      seed,
      rulesJSON: rulesJSON(station.rules),
      confirmReplace: input.confirmReplace ?? false,
      expectedProfileID: input.profileID,
    });
    queued = data.stationStart ?? [];
  } catch (error) {
    if (error instanceof Error && error.message.includes("confirmReplace")) {
      return { status: "needs-confirm" };
    }
    throw error;
  }
  if (!queued.length) return { status: "empty", message: preview.explanation };
  const why = new Map(preview.candidates.map((c) => [c.mediaID, c.explanation]));
  const reasons = Object.fromEntries(queued.map(({ id }) => [id, why.get(id) ?? ""]));
  const started: RadioStation = {
    ...station,
    profileID: input.profileID,
    ruleTitles: input.ruleTitles,
    reasons,
  };
  setActive(started);
  return { status: "started", firstMediaID: queued[0].id, station: started };
}

/**
 * Adds up to RADIO_TOP_UP related Media the queue does not hold yet, and
 * returns how many were added. The server leaves out what is already queued
 * before it samples, so each top-up reaches further than the last. A stop,
 * retune or identity change while this runs stops it before its next write.
 */
export async function topUpRadio(station: RadioStation, queued: ReadonlySet<string>) {
  const mine = generation;
  const live = () => generation === mine && current() === station;
  if (!live()) return 0;
  const { candidates } = await generate(
    { ...station, rules: { ...station.rules, excludeQueued: true } },
    randomSeed(),
  );
  // Media the Station already queued count as queued even before the queue is read again.
  const fresh = candidates
    .filter((c) => !queued.has(c.mediaID) && !(c.mediaID in station.reasons))
    .slice(0, RADIO_TOP_UP);
  const reasons = { ...station.reasons };
  let added = 0;
  for (const candidate of fresh) {
    if (!live()) break;
    try {
      await graphql(ADD, { mediaID: candidate.mediaID, expectedProfileID: station.profileID });
    } catch {
      // A locked or removed item is skipped; the rest still join the queue.
      continue;
    }
    reasons[candidate.mediaID] = candidate.explanation;
    added++;
  }
  if (added && live() && active) {
    // The same Station, now naming why its new items were picked.
    active = { ...active, station: { ...station, reasons } };
    notify();
  }
  return added;
}
