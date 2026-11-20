import type { GenericMediaInfo, LibrarySummary, MediaCredit } from "@/lib/graphql/types";

// Readable media addresses (CORE-2329). A watch or detail URL reads
//
//   /watch/<library-slug>/<readable-slug>-<token>
//
// where the token alone names the item and everything before it is cosmetic.
// The token carries the 17 hex digits from a content-addressed Media ID
// (store.mediaIDForFingerprint). Sampled IDs tag the digest with "sample1."
// so the client can distinguish them from existing SHA-256 addresses. The
// readable words come from the Library's name, the item's own title, and who
// made it or what it belongs
// to. Any other ID shape (an encrypted Library's random IDs among them, whose
// titles must not reach the address bar) keeps the bare-ID address. A stale
// or bare address resolves through its token and is replaced by the
// canonical one once the item is known.

const CONTENT_ID = /^media-(sha256|sample1)-([0-9a-f]{17})$/;
const BARE_SAMPLED_ID = /^media-sample1-([0-9a-f]{17})$/;
const TOKEN = /(?:^|-)(sample1\.)?([0-9a-f]{17})$/;
/** The readable words before the token are capped at this many characters. */
export const MAX_SLUG_LENGTH = 60;

export type MediaRoute = "watch" | "media";

/**
 * slugify folds text to lowercase ASCII words joined by "-": accents are
 * dropped, punctuation and spacing collapse to one "-", and the result is cut
 * at a word boundary within max characters. Text with no Latin letters or
 * digits yields "".
 */
export function slugify(text: string, max = MAX_SLUG_LENGTH): string {
  const words = text
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/['’]/g, "")
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
  let slug = "";
  for (const word of words) {
    const next = slug ? `${slug}-${word}` : word;
    if (next.length > max) return slug || word.slice(0, max);
    slug = next;
  }
  return slug;
}

/** Reads the ID from the route suffix, with a legacy fallback for ambiguous old bookmarks. */
export function mediaAddressIDs(rest: string | undefined): [string, string?] {
  const segments = (rest ?? "").split("/").filter(Boolean);
  const last = segments.at(-1) ?? "";
  let segment = last;
  try {
    segment = decodeURIComponent(last);
  } catch {
    // A malformed escape is read as typed.
  }
  // Older unqualified links for "Media Sample1" have the same spelling as a
  // bare sampled ID. Under a Library, sampled links always use dotted tokens.
  const bareSample = segments.length === 1 ? BARE_SAMPLED_ID.exec(segment) : null;
  if (bareSample) return [segment, `media-sha256-${bareSample[1]}`];
  const token = TOKEN.exec(segment);
  return [token ? `media-${token[1] ? "sample1" : "sha256"}-${token[2]}` : segment];
}

/** The words an item is addressed by: who made it or what it belongs to, then its title. */
export function readableMediaWords(
  media: Pick<GenericMediaInfo, "title" | "mediaClass" | "credits">,
): string {
  const credits: MediaCredit[] = media.credits ?? [];
  // A listening item is named by who made it, anything else by what it
  // belongs to; both are plugin-declared archetypes, not domain words.
  const lead =
    (media.mediaClass === "audio" ? credits.find((c) => c.archetype === "person") : undefined) ??
    credits.find((c) => c.archetype === "container");
  return [lead?.title, media.title].filter(Boolean).join(" ");
}

/** The Library's address word: its name, else its ID without the "library-" prefix. */
export function librarySlug(libraryID: string | undefined, libraries: readonly LibrarySummary[]) {
  if (!libraryID) return "";
  const library = libraries.find((entry) => entry.id === libraryID);
  return slugify(library?.title ?? "") || slugify(libraryID.replace(/^library-/, ""));
}

/**
 * mediaPath is the canonical address of an item on a route. Without readable
 * words (or for an ID that is not content-addressed) it is the bare-ID form.
 */
export function mediaPath(
  route: MediaRoute,
  id: string,
  readable?: { library?: string; words?: string },
): string {
  const content = CONTENT_ID.exec(id);
  if (!content) return `/${route}/${encodeURIComponent(id)}`;
  const slug = slugify(readable?.words ?? "");
  const token = content[1] === "sample1" ? `sample1.${content[2]}` : content[2];
  const library = readable?.library ? `${readable.library}/` : "";
  const qualifiedSample = content[1] === "sample1" && library;
  const ambiguousLegacy = content[1] === "sha256" && !library && slug === "media-sample1";
  const tail = slug && !ambiguousLegacy ? `${slug}-${token}` : qualifiedSample ? token : id;
  return `/${route}/${library}${tail}`;
}

/** The canonical address of a loaded item, or null while it cannot be named. */
export function canonicalMediaPath(
  route: MediaRoute,
  media: Pick<GenericMediaInfo, "id" | "title" | "mediaClass" | "credits" | "libraryID">,
  libraries: readonly LibrarySummary[],
): string {
  return mediaPath(route, media.id, {
    library: librarySlug(media.libraryID, libraries),
    words: readableMediaWords(media),
  });
}
