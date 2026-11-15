import { AlertCircle } from "lucide-react";
import type { RefObject } from "react";

// What the enroll and promote pages share on top of the surface primitives
// (components/beamfall/surface): pairing-code entry and the form feedback.

// The pairing-code field: centred monospace, spaced like the Console shows codes.
export const PAIRING_CODE_INPUT = "text-center font-mono text-xl tracking-[0.3em]";

// Pairing codes are case-insensitive and the server ignores spaces and dashes,
// so typed or pasted input is uppercased and stripped of whitespace as it
// arrives ("abcd efgh" → "ABCDEFGH"); dashes stay visible, as the Console shows them.
export function normalizePairingCode(value: string): string {
  return value.toUpperCase().replace(/\s+/g, "");
}

// The status line and the focusable error alert under each form. The error is
// inline danger text on the card surface, not a tinted box inside the card.
export function FormFeedback({
  status,
  error,
  errorID,
  errorRef,
}: {
  status?: string | null;
  error: string | null;
  errorID: string;
  errorRef: RefObject<HTMLParagraphElement | null>;
}) {
  return (
    <>
      {status && (
        <p role="status" className="text-center text-sm text-muted-foreground">
          {status}
        </p>
      )}
      {error && (
        <p
          ref={errorRef}
          id={errorID}
          role="alert"
          tabIndex={-1}
          className="flex items-start gap-2 text-sm leading-snug text-(--state-danger) outline-none"
        >
          <AlertCircle aria-hidden className="mt-0.5 size-4 shrink-0" />
          {error}
        </p>
      )}
    </>
  );
}
