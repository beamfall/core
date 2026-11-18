import type { MediaCredit } from "@/lib/graphql/types";

// The closing rows of a Media detail page (CORE-2444). The server ranks the
// Related row (relatedMedia) and bounds both reads, so Muted Media stay out.

/** The longest More with or Related row. */
export const RELATED_LIMIT = 20;

/** The person "More with" names: the first person credit in the server's order. */
export function moreWithLead(
  credits: readonly MediaCredit[] | null | undefined,
): MediaCredit | null {
  return credits?.find((credit) => credit.archetype === "person" && credit.entityID) ?? null;
}
