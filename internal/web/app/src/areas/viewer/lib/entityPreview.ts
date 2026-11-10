// Navigation state a link may carry into the entity page so its header can
// paint before the entity request settles. It is bound to the identity that
// rendered the link: history state outlives identity transitions (a Back after
// relock or a Profile switch lands on the old entry), so a preview recorded
// under another identity — a revealed one in particular — is ignored.

export interface EntityPreview {
  title: string;
  kind?: string;
}

interface EntityLinkState {
  entityPreview?: {
    entityID?: unknown;
    identity?: unknown;
    title?: unknown;
    kind?: unknown;
  };
}

/** Builds the navigation state for a link to /entity/:id (see EntityPage). */
export function entityLinkState(
  item: { id: string; title: string; kind?: string },
  identity: string | null,
): EntityLinkState | undefined {
  if (!identity) return undefined;
  return {
    entityPreview: { entityID: item.id, identity, title: item.title, kind: item.kind },
  };
}

/** The preview carried by navigation state, only when it matches this entity and identity. */
export function readEntityPreview(
  state: unknown,
  identity: string | null,
  entityID: string,
): EntityPreview | null {
  if (!identity || !state || typeof state !== "object") return null;
  const preview = (state as EntityLinkState).entityPreview;
  if (!preview || preview.identity !== identity || preview.entityID !== entityID) return null;
  if (typeof preview.title !== "string" || !preview.title) return null;
  return {
    title: preview.title,
    kind: typeof preview.kind === "string" && preview.kind ? preview.kind : undefined,
  };
}
