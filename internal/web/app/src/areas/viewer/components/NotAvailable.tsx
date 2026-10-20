import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// Leak-safe terminal surface for a target the selected Profile cannot reach
// (ADR-0083 §32): a hidden or restricted item must NOT reveal itself through a
// title placeholder, a count, an artwork gap, or a route-specific error. The
// server fails closed — an inaccessible-or-missing target returns null with no
// oracle — so the UI is deliberately indistinguishable between "hidden" and
// "missing": one calm, generic message, no specifics. A deep link does not
// bypass authorization; this is only the honest end state when it resolves to
// nothing this Profile may see.
export function NotAvailable({
  titleKey = "notAvailableTitle",
  leadKey = "notAvailableLead",
}: {
  titleKey?: StringKey;
  leadKey?: StringKey;
}) {
  const t = useT();
  return (
    <div className="bf-empty flex flex-col gap-2" role="status">
      <p className="text-lg font-semibold text-[var(--text-primary)]">{t(titleKey)}</p>
      <p className="text-sm text-[var(--text-secondary)]">{t(leadKey)}</p>
    </div>
  );
}
