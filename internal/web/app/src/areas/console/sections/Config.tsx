import { type FormEvent, useEffect, useState } from "react";
import { ControlRow, ErrorState, Fieldset } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Advanced } from "@/lib/advancedSettings";
import { fetchTranscodeConfig, setTranscodeConfig } from "@/lib/graphql";
import type { TranscodeConfigInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

type TextField = "codec" | "quality" | "device";

const TEXT_FIELDS: { field: TextField; label: StringKey }[] = [
  { field: "codec", label: "transcodeCodec" },
  { field: "quality", label: "transcodeQuality" },
  { field: "device", label: "transcodeDevice" },
];

// Hardware transcoding policy (ADR-0007 §6 / ADR-0010). Empty string values
// fall back to the server default server-side. The on/off switch is the Normal
// control; codec, quality, device and the probe list are Advanced, and a field
// that holds a value, saved or being edited, stays visible in Normal so Save
// never submits a setting out of sight (CORE-1117).
function TranscodeConfig() {
  const t = useT();
  const { data, failed, setData } = useAsync(fetchTranscodeConfig);
  const [form, setForm] = useState<TranscodeConfigInfo>({
    hardwareEnabled: false,
    codec: "",
    quality: "",
    device: "",
    configuredPreference: "",
    hardwareAcceleration: [],
  });

  useEffect(() => {
    if (data) setForm(data);
  }, [data]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    const next = await setTranscodeConfig(form);
    setData(next);
  }

  if (failed) return <ErrorState message="transcodeConfigUnavailable" />;

  return (
    <form onSubmit={onSave} className="space-y-3">
      <Fieldset title="transcodeConfigHeading" help="transcodeConfigLead">
        <ControlRow
          label="transcodeHardwareEnabled"
          control={
            <Switch
              aria-label={t("transcodeHardwareEnabled")}
              checked={form.hardwareEnabled}
              onCheckedChange={(c) => setForm((f) => ({ ...f, hardwareEnabled: c }))}
            />
          }
        />
        {TEXT_FIELDS.map(({ field, label }) => (
          <Advanced key={field} keep={Boolean(form[field])}>
            <ControlRow
              label={label}
              control={
                <Input
                  id={`tc-${field}`}
                  aria-label={t(label)}
                  value={form[field]}
                  placeholder={t("transcodeServerDefault")}
                  className="w-44"
                  onChange={(e) => setForm((f) => ({ ...f, [field]: e.target.value }))}
                />
              }
            />
          </Advanced>
        ))}
        {form.hardwareAcceleration.length > 0 ? (
          <Advanced>
            <div className="grid gap-2 text-sm">
              {form.hardwareAcceleration.map((probe) => (
                <div
                  key={probe.accelerator}
                  className="flex items-center justify-between gap-3 rounded border px-3 py-2"
                >
                  <span className="font-mono">{probe.accelerator}</span>
                  <span className="text-muted-foreground">{probe.status}</span>
                </div>
              ))}
            </div>
          </Advanced>
        ) : null}
      </Fieldset>
      <Button type="submit">{t("transcodeConfigSave")}</Button>
    </form>
  );
}

// Server configuration (ADR-0007 §4). Bootstrap config (ports, data dir,
// secrets) is deliberately not editable here, and the background-worker pool
// lives on Operations alone.
export function Config() {
  return (
    <Section heading="consoleConfigHeading" lead="consoleConfigLead">
      <TranscodeConfig />
    </Section>
  );
}
