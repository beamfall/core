// POWERTOOLS-3 — batch-rename pattern preview (pure, client-side).
//
// The preview is computed locally so the operator sees old→new before any server
// mutation; APPLY then hands the resulting (path, name) pairs to the journaled
// BatchRenameManagedPaths. Server-side validation is still the authority — this is
// only an affordance (the server may reject a name; the preview never invents a
// result).

/** One file the rename targets: its full managed path + current basename. */
export interface RenameSource {
  path: string;
  name: string;
}

export interface RenamePattern {
  /** Literal substring to find in the basename (empty = no find/replace step). */
  find: string;
  /** Replacement for `find`. */
  replace: string;
  /** Optional `{n}` sequence prefix template, e.g. "Episode " → "Episode 01 …". */
  prefix: string;
  /** Starting sequence number for `{n}` / prefix numbering. */
  startAt: number;
  /** Zero-pad width for the sequence number (0 = no padding). */
  pad: number;
}

export const DEFAULT_PATTERN: RenamePattern = {
  find: "",
  replace: "",
  prefix: "",
  startAt: 1,
  pad: 2,
};

/** One previewed rename: the source path plus old and proposed-new basenames. */
export interface RenamePreview {
  path: string;
  oldName: string;
  newName: string;
  /** True when newName differs from oldName (an actual change to apply). */
  changed: boolean;
}

/** Split a basename into stem + extension (extension includes the dot). */
function splitExt(name: string): [string, string] {
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return [name, ""];
  return [name.slice(0, dot), name.slice(dot)];
}

function padNumber(value: number, width: number): string {
  const text = String(value);
  return width > 0 ? text.padStart(width, "0") : text;
}

/**
 * Apply the pattern to one stem at sequence index `seq`. The order is:
 *   1. literal find/replace (all occurrences) on the stem;
 *   2. prefix template — `{n}` in the prefix is replaced by the padded sequence,
 *      otherwise the padded sequence is appended after the prefix text.
 */
function applyToStem(stem: string, pattern: RenamePattern, seq: number): string {
  let next = stem;
  if (pattern.find !== "") {
    next = next.split(pattern.find).join(pattern.replace);
  }
  if (pattern.prefix !== "") {
    const num = padNumber(seq, pattern.pad);
    // `{n}` substitutes the sequence number in place and keeps the stem after the
    // rendered prefix; a prefix with no `{n}` token gets the number appended.
    next = pattern.prefix.includes("{n}")
      ? `${pattern.prefix.split("{n}").join(num)}${next}`
      : `${pattern.prefix}${num} ${next}`;
  }
  return next;
}

/**
 * Compute the old→new preview for every source. Sequence numbering counts from
 * `startAt` across the sources in the given order. Extensions are preserved.
 */
export function previewRenames(
  sources: readonly RenameSource[],
  pattern: RenamePattern,
): RenamePreview[] {
  return sources.map((source, index) => {
    const [stem, ext] = splitExt(source.name);
    const newStem = applyToStem(stem, pattern, pattern.startAt + index);
    const newName = `${newStem}${ext}`;
    return {
      path: source.path,
      oldName: source.name,
      newName,
      changed: newName !== source.name && newName.trim() !== "",
    };
  });
}

/** The (path, name) pairs to send to BatchRenameManagedPaths — changes only. */
export function changedRenames(
  previews: readonly RenamePreview[],
): { path: string; name: string }[] {
  return previews.filter((p) => p.changed).map((p) => ({ path: p.path, name: p.newName }));
}
