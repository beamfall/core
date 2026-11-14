import {
  type KeyboardEvent,
  type PointerEvent,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useT } from "@/lib/i18n";
import type { LibraryItemsLetter } from "../lib/libraryBrowse";

// "#" collects titles that open on a digit or symbol (the server's initial).
export const LETTER_RAIL_OTHER = "#";
const BASE_LETTERS = [LETTER_RAIL_OTHER, ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ"];

// Below 360px wide, or too short for 27 letters at 16px each, the rail is too
// cramped to hit, so a "Jump to" control opens a sheet of letters instead.
const CRAMPED_QUERY = "(max-width: 359px), (max-height: 519px)";

function subscribeCramped(onChange: () => void): () => void {
  if (typeof window.matchMedia !== "function") return () => {};
  const query = window.matchMedia(CRAMPED_QUERY);
  query.addEventListener("change", onChange);
  return () => query.removeEventListener("change", onChange);
}

function isCramped(): boolean {
  return typeof window.matchMedia === "function" && window.matchMedia(CRAMPED_QUERY).matches;
}

/** The rail's letters: "#", A to Z, then any other initial the result holds. */
export function letterRailLetters(index: LibraryItemsLetter[]): string[] {
  const extras = index
    .map((entry) => entry.letter)
    .filter((letter) => !BASE_LETTERS.includes(letter))
    .sort((a, b) => a.localeCompare(b));
  return [...BASE_LETTERS, ...extras];
}

// The A to Z jump rail beside a title-sorted Library grid (CORE-1685), after
// the approved console-list-controls board. The server reports where each
// initial starts and how many titles it holds across the whole result, so a
// jump needs no item loaded first. Letters with no titles stay in place,
// aria-disabled and skipped. The rail is one tab stop: the arrow keys, Home
// and End move and jump; typing a letter jumps to it (or to the next letter
// that has titles); Enter or a click also focuses the first title. Dragging
// the rail shows the letter under the pointer and jumps there on release.
export function LibraryLetterRail({
  letters,
  current,
  onJump,
}: {
  letters: LibraryItemsLetter[];
  /** The letter at the top of the grid, highlighted as the grid scrolls. */
  current: string | null;
  onJump: (offset: number, focusItem: boolean) => void;
}) {
  const t = useT();
  const cramped = useSyncExternalStore(subscribeCramped, isCramped, () => false);
  const [selected, setSelected] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const [bubble, setBubble] = useState<{ letter: string; top: number } | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  const buttonsRef = useRef(new Map<string, HTMLButtonElement>());
  // Drag state: the letter under the press, whether the pointer has crossed
  // to another letter (a scrub, not a click), and the letter it is over.
  const pressRef = useRef<string | null>(null);
  const scrubbingRef = useRef(false);
  const scrubLetterRef = useRef<string | null>(null);

  const byLetter = new Map(letters.map((entry) => [entry.letter, entry]));
  const all = letterRailLetters(letters);
  const enabled = all.filter((letter) => byLetter.has(letter));
  if (enabled.length === 0) return null;

  const tabStop =
    selected && byLetter.has(selected)
      ? selected
      : current && byLetter.has(current)
        ? current
        : enabled[0];

  const name = (letter: string) =>
    letter === LETTER_RAIL_OTHER ? t("libraryLetterOther") : letter;
  const label = (letter: string) => {
    const entry = byLetter.get(letter);
    return entry
      ? t("libraryLetterTitles", { letter: name(letter), count: entry.count })
      : t("libraryLetterEmpty", { letter: name(letter) });
  };

  function jump(letter: string, focusItem: boolean) {
    const entry = byLetter.get(letter);
    if (!entry) return;
    setSelected(letter);
    onJump(entry.offset, focusItem);
  }

  function moveTo(letter: string) {
    setSelected(letter);
    buttonsRef.current.get(letter)?.focus();
    jump(letter, false);
  }

  // The first letter with titles at or after `from` in rail order, else the
  // last one before it.
  function nearestEnabled(from: number): string | undefined {
    return (
      all.slice(from).find((l) => byLetter.has(l)) ?? enabled.findLast((l) => all.indexOf(l) < from)
    );
  }

  function step(letter: string, direction: 1 | -1): string {
    const at = all.indexOf(letter);
    for (let k = at + direction; k >= 0 && k < all.length; k += direction) {
      if (byLetter.has(all[k])) return all[k];
    }
    return letter;
  }

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>, letter: string) {
    let target: string | undefined;
    if (event.key === "ArrowDown" || event.key === "ArrowRight") target = step(letter, 1);
    else if (event.key === "ArrowUp" || event.key === "ArrowLeft") target = step(letter, -1);
    else if (event.key === "Home") target = enabled[0];
    else if (event.key === "End") target = enabled[enabled.length - 1];
    else if (/^[a-z0-9]$/i.test(event.key) && !event.metaKey && !event.ctrlKey && !event.altKey) {
      const typed = /[0-9]/.test(event.key) ? LETTER_RAIL_OTHER : event.key.toUpperCase();
      target = nearestEnabled(all.indexOf(typed));
      if (target) {
        setAnnouncement(
          target === typed
            ? t("libraryLetterShowing", { letter: name(typed) })
            : t("libraryLetterSkipped", { letter: name(typed), next: name(target) }),
        );
      }
    } else return;
    event.preventDefault();
    if (target) moveTo(target);
  }

  // The letter with titles nearest the pointer, from the buttons' geometry.
  function letterAt(clientY: number): string | null {
    let best: string | null = null;
    let bestDistance = Number.POSITIVE_INFINITY;
    for (const letter of enabled) {
      const rect = buttonsRef.current.get(letter)?.getBoundingClientRect();
      if (!rect) continue;
      const distance = clientY < rect.top ? rect.top - clientY : Math.max(0, clientY - rect.bottom);
      if (distance < bestDistance) {
        best = letter;
        bestDistance = distance;
      }
    }
    return best;
  }

  function onPointerDown(event: PointerEvent<HTMLElement>) {
    if (event.button !== 0) return;
    pressRef.current = letterAt(event.clientY);
    scrubbingRef.current = false;
    scrubLetterRef.current = null;
  }

  function onPointerMove(event: PointerEvent<HTMLElement>) {
    if (pressRef.current === null) return;
    const letter = letterAt(event.clientY);
    if (!letter) return;
    if (!scrubbingRef.current) {
      if (letter === pressRef.current) return;
      // Capture only once it is a scrub, so a plain click still lands on its button.
      scrubbingRef.current = true;
      event.currentTarget.setPointerCapture?.(event.pointerId);
    }
    scrubLetterRef.current = letter;
    const railTop = event.currentTarget.getBoundingClientRect().top;
    setBubble({ letter, top: Math.max(0, event.clientY - railTop - 32) });
  }

  function endPress(commit: boolean) {
    const letter = scrubLetterRef.current;
    if (commit && scrubbingRef.current && letter) moveTo(letter);
    pressRef.current = null;
    scrubLetterRef.current = null;
    setBubble(null);
    // The click that may follow a scrub's release is swallowed; clear the flag
    // after it so the next keyboard activation is not.
    window.setTimeout(() => {
      scrubbingRef.current = false;
    }, 0);
  }

  if (cramped) {
    return (
      <>
        <button
          type="button"
          className="bf-letter-rail-trigger"
          aria-label={t("libraryLetterRailLabel")}
          aria-haspopup="dialog"
          onClick={() => setSheetOpen(true)}
        >
          {t("libraryLetterTrigger")}
        </button>
        <Dialog open={sheetOpen} onOpenChange={setSheetOpen}>
          <DialogContent finalFocus={false}>
            <DialogHeader>
              <DialogTitle>{t("libraryLetterRailLabel")}</DialogTitle>
            </DialogHeader>
            <ul className="bf-letter-sheet">
              {all.map((letter) => (
                <li key={letter}>
                  <button
                    type="button"
                    aria-disabled={byLetter.has(letter) ? undefined : true}
                    aria-label={label(letter)}
                    onClick={() => {
                      if (!byLetter.has(letter)) return;
                      setSheetOpen(false);
                      jump(letter, true);
                    }}
                  >
                    {letter}
                  </button>
                </li>
              ))}
            </ul>
          </DialogContent>
        </Dialog>
      </>
    );
  }

  return (
    <nav
      aria-label={t("libraryLetterRailLabel")}
      className="bf-letter-rail"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={() => endPress(true)}
      onPointerCancel={() => endPress(false)}
    >
      <ul>
        {all.map((letter) => {
          const has = byLetter.has(letter);
          return (
            <li key={letter}>
              <button
                ref={(node) => {
                  if (node) buttonsRef.current.set(letter, node);
                  else buttonsRef.current.delete(letter);
                }}
                type="button"
                aria-disabled={has ? undefined : true}
                aria-current={has && letter === current ? "true" : undefined}
                tabIndex={letter === tabStop ? 0 : -1}
                aria-label={label(letter)}
                onKeyDown={(event) => onKeyDown(event, letter)}
                onClick={() => {
                  if (scrubbingRef.current || !has) return;
                  jump(letter, true);
                }}
              >
                {letter}
              </button>
            </li>
          );
        })}
      </ul>
      {bubble && (
        <div className="bf-letter-bubble" style={{ top: bubble.top }} aria-hidden="true">
          {bubble.letter}
          <small>
            {t("libraryLetterCount", { count: byLetter.get(bubble.letter)?.count ?? 0 })}
          </small>
        </div>
      )}
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>
    </nav>
  );
}
