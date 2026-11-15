import type { ReactNode } from "react";
import { CollapsibleCard } from "@/components/beamfall/workbench";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// The shared chrome for one Console screen: a heading, an optional lead, an
// optional right-aligned action cluster (e.g. a Refresh button), and the
// screen body. Keeps every section visually consistent (ADR-0007 §4) in the
// Workbench register without each one re-implementing the header. A
// secondary section can be `collapsible` (CORE-1123): it then renders as a
// CollapsibleCard, closed unless `defaultOpen`, so a long screen opens short.
export function Section({
  heading,
  lead,
  actions,
  collapsible = false,
  defaultOpen = false,
  children,
}: {
  heading: StringKey;
  lead?: StringKey;
  actions?: ReactNode;
  collapsible?: boolean;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const t = useT();
  if (collapsible) {
    return (
      <CollapsibleCard
        className="console-section"
        title={t(heading)}
        lead={lead && t(lead)}
        actions={actions}
        defaultOpen={defaultOpen}
      >
        {children}
      </CollapsibleCard>
    );
  }
  return (
    <section className="console-section space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-2">
        <div className="min-w-0 space-y-1">
          <h2 className="text-[20px] font-semibold leading-7">{t(heading)}</h2>
          {lead && <p className="max-w-[68ch] text-sm text-muted-foreground">{t(lead)}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}
