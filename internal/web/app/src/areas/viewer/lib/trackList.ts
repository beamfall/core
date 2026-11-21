import type { MediaRow } from "@/lib/graphql/browse";
import type {
  EntityInfo,
  GenericMediaInfo,
  MediaCredit,
  RelatedEntityGroup,
  SourceItem,
} from "@/lib/graphql/types";
import {
  catalogQueueState,
  type PlayerNavState,
  type PlayQueueItem,
  playQueueOf,
} from "./watchQueue";

// Listening layout rules (CORE-1635). Core stays domain-agnostic: the layout is
// chosen from the Entity's archetype, the Media class the server reports and
// the server's own ordering — never from a plugin's kind names or field names.
//
// - A "container" Entity whose member Media are all class "audio" renders as a
//   numbered track list in server order (mediaForEntity order). When core's
//   scan stored a "sequence" on every track and they hold at least two values,
//   the list groups by sequence, ascending (a multi-disc album). Within a group
//   (or the whole list) the stored "index" numbers and orders the rows when
//   every row has one and none repeats; otherwise rows keep server order and
//   are numbered from 1. A row names the
//   track's "person" credits only when they differ from the container's own
//   people: its related "person" Entities, or, when it declares none, the people
//   credited on every track.
// - A "person" Entity lists its related "container" Entities as usual, then
//   "Appears on": containers credited on the person's own Media that are not
//   among its related containers (a compilation credits another album person).
// - Top tracks: the profile's most-played feed narrowed to the person's Media,
//   filled up from server order.

export const AUDIO_CLASS = "audio";
export const TOP_TRACKS_LIMIT = 10;

type ClassedItem = SourceItem & { mediaClass?: string };

/** One row of a track list. */
export interface TrackRow {
  item: SourceItem;
  credits?: readonly MediaCredit[];
  /** The stored index for an album when usable, else the 1-based display position. */
  number: number;
  /** The stored sequence this row groups under; set only when the list is grouped. */
  group?: number;
  /** Credited people, shown only when they differ from the listing's own. */
  artist?: string;
  durationSeconds?: number;
}

/** The class of a listed Media item: the row's own report, else its media(id) read. */
export function mediaClassOf(
  item: ClassedItem,
  rows: Readonly<Record<string, GenericMediaInfo>>,
): string | undefined {
  return item.mediaClass || rows[item.id]?.mediaClass;
}

/** True when there is at least one Media item and every one is audio. */
export function isAudioListing(
  items: readonly ClassedItem[],
  rows: Readonly<Record<string, GenericMediaInfo>>,
): boolean {
  const media = items.filter((item) => item.kind === "media");
  return media.length > 0 && media.every((item) => mediaClassOf(item, rows) === AUDIO_CLASS);
}

/** True when some listed Media item is known not to be audio (so no row read is needed). */
export function hasKnownNonAudio(items: readonly ClassedItem[]): boolean {
  return items.some(
    (item) => item.kind === "media" && !!item.mediaClass && item.mediaClass !== AUDIO_CLASS,
  );
}

function personCredits(row: GenericMediaInfo | undefined) {
  return (row?.credits ?? []).filter((credit) => credit.archetype === "person");
}

/** The container's own people: related "person" Entities, else those credited on every track. */
export function ownerPeople(
  groups: readonly RelatedEntityGroup[] | undefined,
  items: readonly SourceItem[],
  rows: Readonly<Record<string, GenericMediaInfo>>,
): Set<string> {
  const related = new Set<string>();
  for (const group of groups ?? []) {
    if (group.archetype !== "person") continue;
    for (const item of group.items) related.add(item.id);
  }
  if (related.size > 0) return related;
  const media = items.filter((item) => item.kind === "media");
  const credited = media.map(
    (item) => new Set(personCredits(rows[item.id]).map((credit) => credit.entityID)),
  );
  if (credited.length === 0) return related;
  return new Set([...credited[0]].filter((id) => credited.every((ids) => ids.has(id))));
}

/** A listed disc or track position, kept only when positive, matching the per-Media row reader. */
function usablePosition(value: number | null | undefined): number | undefined {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : undefined;
}

/** The sequence of every listed item, with a per-Media fallback, or null when the list does not group. */
function groupSequences(
  media: readonly SourceItem[],
  rows: Readonly<Record<string, MediaRow>>,
): number[] | null {
  const sequences = media.map((item) => usablePosition(item.sequence) ?? rows[item.id]?.sequence);
  if (sequences.some((sequence) => sequence === undefined)) return null;
  return new Set(sequences).size > 1 ? (sequences as number[]) : null;
}

/** One group's items in display order with their numbers. */
function numberGroup(
  media: readonly SourceItem[],
  rows: Readonly<Record<string, MediaRow>>,
): { item: SourceItem; number: number }[] {
  const indexes = media.map((item) => usablePosition(item.index) ?? rows[item.id]?.index);
  const stored =
    indexes.every((index) => index !== undefined) && new Set(indexes).size === indexes.length;
  if (!stored) return media.map((item, position) => ({ item, number: position + 1 }));
  return media
    .map((item, position) => ({ item, number: indexes[position] as number }))
    .sort((a, b) => a.number - b.number);
}

function trackRow(
  item: SourceItem,
  row: MediaRow | undefined,
  owners: ReadonlySet<string>,
  number: number,
  group?: number,
): TrackRow {
  const people = personCredits(row);
  const differs = people.some((credit) => !owners.has(credit.entityID));
  const track: TrackRow = { item, number };
  if (row) track.credits = row.credits ?? [];
  if (group !== undefined) track.group = group;
  if (differs) track.artist = people.map((credit) => credit.title).join(", ");
  if (row?.durationSeconds) track.durationSeconds = row.durationSeconds;
  return track;
}

/** Person listings use their supplied ranking or server order for display and playback. */
export function listedTrackRows(
  items: readonly SourceItem[],
  rows: Readonly<Record<string, MediaRow>>,
  owners: ReadonlySet<string>,
): TrackRow[] {
  return items.map((item, position) => trackRow(item, rows[item.id], owners, position + 1));
}

/**
 * The track rows of an audio container in display order: grouped by stored
 * sequence when the list groups, numbered by stored index or by position.
 */
export function albumTrackRows(
  items: readonly SourceItem[],
  rows: Readonly<Record<string, MediaRow>>,
  owners: ReadonlySet<string>,
): TrackRow[] {
  const media = items.filter((item) => item.kind === "media");
  const sequences = groupSequences(media, rows);
  const groups: { group?: number; media: SourceItem[] }[] = [];
  if (sequences) {
    for (const sequence of [...new Set(sequences)].sort((a, b) => a - b)) {
      groups.push({
        group: sequence,
        media: media.filter((_, position) => sequences[position] === sequence),
      });
    }
  } else {
    groups.push({ media });
  }
  return groups.flatMap(({ group, media: members }) =>
    numberGroup(members, rows).map(({ item, number }) =>
      trackRow(item, rows[item.id], owners, number, group),
    ),
  );
}

/**
 * The track rows an audio container lists, in display order, or null when the
 * Entity is not an audio container. The page's primary Play action and the
 * track list both read this order, so they start and queue the same rows.
 */
export function containerTrackRows(
  entity: EntityInfo | null,
  items: readonly SourceItem[],
  rows: Readonly<Record<string, MediaRow>> | null,
): TrackRow[] | null {
  if (!entity || !rows || entity.archetype !== "container") return null;
  if (!isAudioListing(items, rows)) return null;
  return albumTrackRows(items, rows, ownerPeople(entity.relatedEntities, items, rows));
}

/** A play queue of rows the page verified as audio, so the player knows their class. */
export function audioQueue(items: readonly SourceItem[]): PlayQueueItem[] {
  return playQueueOf(items.map((item) => ({ ...item, mediaClass: AUDIO_CLASS })));
}

/** Consecutive rows that share a group, in order; one ungrouped run when none is grouped. */
export function trackGroups(tracks: readonly TrackRow[]): { group?: number; rows: TrackRow[] }[] {
  const out: { group?: number; rows: TrackRow[] }[] = [];
  for (const track of tracks) {
    const last = out[out.length - 1];
    if (last && last.group === track.group) last.rows.push(track);
    else out.push({ group: track.group, rows: [track] });
  }
  return out;
}

/** m:ss, or h:mm:ss from an hour up; empty when unknown. */
export function formatDuration(seconds: number | undefined): string {
  if (!seconds || seconds < 0) return "";
  const total = Math.round(seconds);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${s}` : `${m}:${s}`;
}

/**
 * Containers credited on a person's Media that are not among its related
 * containers, in first-credit order, as entity items a card can open.
 */
export function appearsOn(
  groups: readonly RelatedEntityGroup[] | undefined,
  items: readonly SourceItem[],
  rows: Readonly<Record<string, GenericMediaInfo>>,
): SourceItem[] {
  const own = new Set<string>();
  for (const group of groups ?? []) {
    if (group.archetype !== "container") continue;
    for (const item of group.items) own.add(item.id);
  }
  const seen = new Set<string>();
  const out: SourceItem[] = [];
  for (const item of items) {
    for (const credit of rows[item.id]?.credits ?? []) {
      if (credit.archetype !== "container" || own.has(credit.entityID)) continue;
      if (seen.has(credit.entityID)) continue;
      seen.add(credit.entityID);
      out.push({ id: credit.entityID, kind: "entity", title: credit.title });
    }
  }
  return out;
}

/**
 * The person's top tracks: their Media in the profile's play-count order, then
 * the rest in server order, capped at `limit`.
 */
export function topTracks(
  items: readonly SourceItem[],
  profileTop: readonly SourceItem[],
  limit = TOP_TRACKS_LIMIT,
): { tracks: SourceItem[]; hasPlays: boolean } {
  const media = items.filter((item) => item.kind === "media");
  const byID = new Map(media.map((item) => [item.id, item]));
  const out: SourceItem[] = [];
  const taken = new Set<string>();
  for (const ranked of profileTop) {
    const item = byID.get(ranked.id);
    if (!item || taken.has(item.id)) continue;
    taken.add(item.id);
    out.push(item);
  }
  for (const item of media) {
    if (!taken.has(item.id)) out.push(item);
  }
  return { tracks: out.slice(0, limit), hasPlays: taken.size > 0 };
}

/** The watch-route state that starts `item` with `queue` as the player's queue. */
export function playState(
  identity: string | null | undefined,
  queue: readonly PlayQueueItem[],
  item: { id: string; title: string },
): PlayerNavState {
  return { title: item.title, ...catalogQueueState(identity, queue, item.id) };
}
