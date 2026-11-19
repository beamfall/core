import { useState } from "react";
import { EmptyState, ErrorState, HelpTip, StatusChip } from "@/components/beamfall/workbench";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { fetchCapabilityStates, setCapabilityEnabled } from "@/lib/graphql";
import type { CapabilityStateInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { publishManageCapabilities } from "@/lib/manageCapabilities";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

// Capability runtime-gating control surface (ADR-0124 §5). Workflow capabilities
// are enabled by config and flippable at runtime here (an audited, attributed,
// Admin-Session-gated mutation). Probe-derived capabilities are auto-detected and
// rendered read-only: a probe-absent capability shows its degraded reason and NO
// toggle (it is never operator-flipped). This whole section is suppressed on App
// Store binaries — both the toggle UI here AND the server API (invariant 8) — so
// it is only reachable on the web Console / direct-distribution Manager.
// Plain names and help for the capabilities this server ships (CORE-2445). An id
// not listed here (a newer server) still shows by its raw id.
const CAPABILITY_TEXT: Record<string, { label: StringKey; tip: StringKey }> = {
  conversion: { label: "capabilityConversion", tip: "capabilityConversionTip" },
  rename: { label: "capabilityRename", tip: "capabilityRenameTip" },
  "share-mount": { label: "capabilityShareMount", tip: "capabilityShareMountTip" },
};

function CapabilityName({ id }: { id: string }) {
  const t = useT();
  const text = CAPABILITY_TEXT[id];
  if (!text) return <span className="console-control-label min-w-0 font-medium">{id}</span>;
  return (
    <span className="console-control-label bf-label-with-tip min-w-0 font-medium">
      {t(text.label)}
      <HelpTip label={t(text.label)} text={text.tip} />
    </span>
  );
}

export function Capabilities() {
  const t = useT();
  const { data, loading, failed, setData } = useAsync(fetchCapabilityStates);
  const [busy, setBusy] = useState<string | null>(null);

  async function toggle(id: string, enabled: boolean) {
    setBusy(id);
    try {
      const next = await setCapabilityEnabled(id, enabled);
      setData(next);
      publishManageCapabilities(next);
    } finally {
      setBusy(null);
    }
  }

  const states: CapabilityStateInfo[] = data ?? [];
  const workflow = states.filter((s) => s.kind === "workflow");
  const probe = states.filter((s) => s.kind === "probe");

  return (
    <Section heading="navOptionalFeatures" lead="optionalFeaturesLead">
      {loading ? (
        <Skeleton className="h-32 w-full" />
      ) : failed ? (
        <ErrorState message="capabilitiesUnavailable" />
      ) : states.length === 0 ? (
        <EmptyState message="capabilitiesEmpty" />
      ) : (
        <div className="space-y-4">
          {workflow.length > 0 && (
            <div className="space-y-2">
              <div className="console-control-label font-medium">
                {t("capabilitiesWorkflowGroup")}
              </div>
              <div className="console-card console-card-pad space-y-3">
                {workflow.map((s) => (
                  <div
                    key={s.id}
                    className="console-control-row"
                    data-setting={`console.identification.${s.id}`}
                  >
                    {/* The switch is the state; no chip repeats it (CORE-1117). */}
                    <CapabilityName id={s.id} />
                    <div className="flex shrink-0 items-center gap-2">
                      <Switch
                        checked={s.enabled}
                        disabled={busy === s.id}
                        onCheckedChange={(c) => void toggle(s.id, c)}
                        aria-label={CAPABILITY_TEXT[s.id] ? t(CAPABILITY_TEXT[s.id].label) : s.id}
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
                  <div
                    key={s.id}
                    className="console-control-row"
                    data-setting={`console.identification.${s.id}`}
                  >
                    <div className="min-w-0 flex items-center gap-2">
                      <CapabilityName id={s.id} />
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
