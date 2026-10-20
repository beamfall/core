import { Layers } from "lucide-react";
import { useState } from "react";
import { EmptyState, ErrorState, StatusChip } from "@/components/beamfall/workbench";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { fetchCapabilityStates, setCapabilityEnabled } from "@/lib/graphql";
import type { CapabilityStateInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Capability runtime-gating control surface (ADR-0124 §5). Workflow capabilities
// are enabled by config and flippable at runtime here (an audited, attributed,
// Admin-Session-gated mutation). Probe-derived capabilities are auto-detected and
// rendered read-only: a probe-absent capability shows its degraded reason and NO
// toggle (it is never operator-flipped). This whole section is suppressed on App
// Store binaries — both the toggle UI here AND the server API (invariant 8) — so
// it is only reachable on the web Console / direct-distribution Manager.
export function Capabilities() {
  const t = useT();
  const { data, loading, failed, setData } = useAsync(fetchCapabilityStates);
  const [busy, setBusy] = useState<string | null>(null);

  async function toggle(id: string, enabled: boolean) {
    setBusy(id);
    try {
      const next = await setCapabilityEnabled(id, enabled);
      setData(next);
    } finally {
      setBusy(null);
    }
  }

  const states: CapabilityStateInfo[] = data ?? [];
  const workflow = states.filter((s) => s.kind === "workflow");
  const probe = states.filter((s) => s.kind === "probe");

  return (
    <Section heading="capabilitiesHeading" lead="capabilitiesLead">
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="capabilitiesUnavailable" />
      ) : states.length === 0 ? (
        <EmptyState message="capabilitiesEmpty" icon={Layers} />
      ) : (
        <div className="space-y-4">
          {workflow.length > 0 && (
            <div className="space-y-2">
              <div className="console-control-label font-medium">
                {t("capabilitiesWorkflowGroup")}
              </div>
              <div className="console-card console-card-pad space-y-3">
                {workflow.map((s) => (
                  <div key={s.id} className="console-control-row">
                    <div className="min-w-0 flex items-center gap-2">
                      <span className="console-control-label font-medium">{s.id}</span>
                      <StatusChip tone={s.enabled ? "green" : "neutral"}>
                        {t(s.enabled ? "capabilitiesEnabled" : "capabilitiesDisabled")}
                      </StatusChip>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                      <Switch
                        checked={s.enabled}
                        disabled={busy === s.id}
                        onCheckedChange={(c) => void toggle(s.id, c)}
                        aria-label={s.id}
                      />
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          {probe.length > 0 && (
            <div className="space-y-2">
              <div className="console-control-label font-medium">{t("capabilitiesProbeGroup")}</div>
              <div className="console-card console-card-pad space-y-3">
                {probe.map((s) => (
                  <div key={s.id} className="console-control-row">
                    <div className="min-w-0 flex items-center gap-2">
                      <span className="console-control-label font-medium">{s.id}</span>
                      {/* Probe-derived: read-only, no toggle. A probe-absent
                          capability surfaces its degraded reason (ADR-0124 §5). */}
                      {s.enabled ? (
                        <StatusChip tone="green">{t("capabilitiesEnabled")}</StatusChip>
                      ) : (
                        <StatusChip tone="amber">{t("capabilitiesProbeAbsent")}</StatusChip>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </Section>
  );
}
