import type { ReactNode } from "react";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

// The shared chrome for one Console screen: a heading, an optional lead, an
// optional right-aligned action cluster (e.g. a Refresh button), and the
// screen body. Keeps every section visually consistent (ADR-0007 §4) in the
// Workbench register without each one re-implementing the header.
export function Section({
  heading,
  lead,
  actions,
  children,
}: {
  heading: StringKey;
  lead?: StringKey;
  actions?: ReactNode;
  children: ReactNode;
}) {
  const t = useT();
  return (
    <section className="console-section space-y-3">
      <div className="flex items-end justify-between gap-4">
        <div className="space-y-0.5">
          <h2 className="text-[15px] font-semibold leading-snug">{t(heading)}</h2>
          {lead && (
            <p className="max-w-2xl text-[13px] leading-snug text-muted-foreground">{t(lead)}</p>
          )}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
      </div>
      {children}
    </section>
  );
}

export function AdvancedDisclosure({ children }: { children: ReactNode }) {
  const t = useT();
  return (
    <details className="console-disclosure">
      <summary>{t("advanced")}</summary>
      <div className="console-disclosure-body">{children}</div>
    </details>
  );
}
