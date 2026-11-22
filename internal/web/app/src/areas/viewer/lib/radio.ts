import { useSyncExternalStore } from "react";
import { graphql } from "@/lib/api";

// Radio (CORE-2772): a Station started from one Media or one Entity fills the
// Profile's Play Queue with related Media and keeps topping it up while it
// plays. The server's Station generator picks the Media and says why each was
// picked; this module starts a Station, remembers it for the Profile that
// started it, and adds more when the queue runs low. The queue itself stays the
// server's Play Queue, so the player walks it the usual way.

/** What a Station starts from: one Media item, or one Entity (a person, container or category). */
export type RadioSeedKind = "media" | "entity";

/** Hard Station rules: only Media from these decades, only Media credited to one of these Entities. */
export interface RadioRules {
  decades?: number[];
  anyEntityIDs?: string[];
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
const START = `mutation RadioStart($seedKind: String!, $seedID: String!, $seed: String, $rulesJSON: String, $confirmReplace: Boolean) {
  stationStart(seedKind: $seedKind, seedID: $seedID, seed: $seed, rulesJSON: $rulesJSON, confirmReplace: $confirmReplace) { id }
}`;
const ADD = `mutation RadioAdd($mediaID: String!) { queueAdd(mediaID: $mediaID) { id } }`;

const STORAGE_KEY = "beamfall.radio";
/** At most this many Media are added per top-up, so one top-up stays a few requests. */
export const RADIO_TOP_UP = 10;
/** A top-up starts when this few playable items remain after the current one. */
export const RADIO_LOW_WATER = 5;

function randomSeed(): string {
  return Math.random().toString(36).slice(2);
}

function rulesJSON(rules: RadioRules): string {
  const cleaned: RadioRules = {};
  if (rules.decades?.length) cleaned.decades = rules.decades;
  if (rules.anyEntityIDs?.length) cleaned.anyEntityIDs = rules.anyEntityIDs;
  return JSON.stringify(cleaned);
}

async function generate(station: Omit<RadioStation, "reasons" | "profileID">, seed: string) {
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

// --- The active Station, one per browser tab -------------------------------

let active: RadioStation | null = readStored();
const listeners = new Set<() => void>();

function readStored(): RadioStation | null {
  try {
    const raw = globalThis.sessionStorage?.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as RadioStation;
    return parsed && typeof parsed.profileID === "string" && typeof parsed.seedID === "string"
      ? parsed
      : null;
  } catch {
    return null;
  }
}

function setActive(next: RadioStation | null) {
  active = next;
  try {
    if (next) globalThis.sessionStorage?.setItem(STORAGE_KEY, JSON.stringify(next));
    else globalThis.sessionStorage?.removeItem(STORAGE_KEY);
  } catch {
    // Storage is a convenience: the Station still runs for this page's life.
  }
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** The Station playing for this Profile, or null. */
export function activeRadio(profileID: string | null | undefined): RadioStation | null {
  return profileID && active?.profileID === profileID ? active : null;
}

export function useActiveRadio(profileID: string | null | undefined): RadioStation | null {
  const station = useSyncExternalStore(
    subscribe,
    () => active,
    () => null,
  );
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
  // preview's reasons describe exactly what was queued.
  const seed = randomSeed();
  const preview = await generate(station, seed);
  if (!preview.candidates.length) return { status: "empty", message: preview.explanation };
  try {
    await graphql(START, {
      seedKind: station.seedKind,
      seedID: station.seedID,
      seed,
      rulesJSON: rulesJSON(station.rules),
      confirmReplace: input.confirmReplace ?? false,
    });
  } catch (error) {
    if (error instanceof Error && error.message.includes("confirmReplace")) {
      return { status: "needs-confirm" };
    }
    throw error;
  }
  const reasons = Object.fromEntries(preview.candidates.map((c) => [c.mediaID, c.explanation]));
  const started: RadioStation = {
    ...station,
    profileID: input.profileID,
    ruleTitles: input.ruleTitles,
    reasons,
  };
  setActive(started);
  return { status: "started", firstMediaID: preview.candidates[0].mediaID, station: started };
}

/**
 * Adds up to RADIO_TOP_UP related Media the queue does not hold yet, and
 * returns how many were added. A fresh seed reshuffles the related set, so
 * each top-up reaches further than the window the Station started with.
 */
export async function topUpRadio(station: RadioStation, queued: ReadonlySet<string>) {
  const { candidates } = await generate(station, randomSeed());
  // Media the Station already queued count as queued even before the queue is read again.
  const fresh = candidates
    .filter((c) => !queued.has(c.mediaID) && !(c.mediaID in station.reasons))
    .slice(0, RADIO_TOP_UP);
  const reasons = { ...station.reasons };
  let added = 0;
  for (const candidate of fresh) {
    try {
      await graphql(ADD, { mediaID: candidate.mediaID });
    } catch {
      // A locked or removed item is skipped; the rest still join the queue.
      continue;
    }
    reasons[candidate.mediaID] = candidate.explanation;
    added++;
  }
  // Only the Station that asked keeps the reasons: a Station started meanwhile replaced it.
  if (added && active === station) setActive({ ...station, reasons });
  return added;
}
