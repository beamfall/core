import { type FormEvent, useEffect, useState } from "react";
import { ControlRow, ErrorState, Fieldset } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { Switch } from "@/components/ui/switch";
import {
  fetchConsoleConfig,
  fetchTranscodeConfig,
  setConsoleConfig,
  setTranscodeConfig,
} from "@/lib/graphql";
import type { TranscodeConfigInfo } from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import { AdvancedDisclosure, Section } from "../Section";
import { useAsync } from "../useAsync";

// Server configuration (ADR-0013): the background-worker pool size. Bootstrap
// config (ports, data dir, secrets) is deliberately not editable here.
function ServerConfig() {
  const t = useT();
  const { data, failed, setData } = useAsync(fetchConsoleConfig);
  const [workers, setWorkers] = useState("");

  useEffect(() => {
    if (data) setWorkers(String(data.backgroundWorkers));
  }, [data]);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    const n = Number(workers);
    if (!Number.isInteger(n) || n < 0) return;
    const next = await setConsoleConfig({ backgroundWorkers: n });
    setData(next);
  }

  return (
    <Section heading="consoleConfigHeading" lead="consoleConfigLead">
      {failed ? (
        <ErrorState message="consoleConfigUnavailable" />
      ) : (
        <form onSubmit={onSave}>
          <Fieldset title="consoleConfigHeading" help="consoleConfigLead">
            <ControlRow
              label="consoleConfigBackgroundWorkers"
              control={
                <Input
                  id="console-workers"
                  aria-label={t("consoleConfigBackgroundWorkers")}
                  type="number"
                  min={0}
                  value={workers}
                  onChange={(e) => setWorkers(e.target.value)}
                  className="w-24"
                />
              }
            />
          </Fieldset>
          <div className="mt-3">
            <Button type="submit">{t("consoleConfigSave")}</Button>
          </div>
        </form>
      )}
    </Section>
  );
}

// Hardware transcoding policy (ADR-0007 §6 / ADR-0010). Empty string values
// fall back to the server default server-side.
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

  return (
    <Section heading="transcodeConfigHeading" lead="transcodeConfigLead">
      {failed ? (
        <ErrorState message="transcodeConfigUnavailable" />
      ) : (
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
            <ControlRow
              label="transcodeCodec"
              control={
                <Input
                  id="tc-codec"
                  aria-label={t("transcodeCodec")}
                  value={form.codec}
                  placeholder={t("transcodeServerDefault")}
                  className="w-44"
                  onChange={(e) => setForm((f) => ({ ...f, codec: e.target.value }))}
                />
              }
            />
            <ControlRow
              label="transcodeQuality"
              control={
                <Input
                  id="tc-quality"
                  aria-label={t("transcodeQuality")}
                  value={form.quality}
                  placeholder={t("transcodeServerDefault")}
                  className="w-44"
                  onChange={(e) => setForm((f) => ({ ...f, quality: e.target.value }))}
                />
              }
            />
            <ControlRow
              label="transcodeDevice"
              control={
                <Input
                  id="tc-device"
                  aria-label={t("transcodeDevice")}
                  value={form.device}
                  placeholder={t("transcodeServerDefault")}
                  className="w-44"
                  onChange={(e) => setForm((f) => ({ ...f, device: e.target.value }))}
                />
              }
            />
            {form.hardwareAcceleration.length > 0 ? (
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
            ) : null}
          </Fieldset>
          <Button type="submit">{t("transcodeConfigSave")}</Button>
        </form>
      )}
    </Section>
  );
}

// Identification & config (ADR-0007 §4): server runtime config + transcode policy.
export function Config() {
  return (
    <div className="space-y-8">
      <ServerConfig />
      <Separator />
      <AdvancedDisclosure>
        <TranscodeConfig />
      </AdvancedDisclosure>
    </div>
  );
}
