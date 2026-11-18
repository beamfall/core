import type { Preset } from "@/components/beamfall/workbench";
import { useT } from "@/lib/i18n";

// Quality presets for a remote bitrate limit (CORE-2445), shared by the
// server-wide limit (Remote access) and a profile's own limit (Schedules). The
// value is bits per second; 0 means no limit. A saved value that is not a preset
// is shown in Mbit/s by `format`.
export function useBitratePresets(): { presets: Preset[]; format: (bps: number) => string } {
  const t = useT();
  const format = (bps: number) =>
    bps === 0 ? t("accessHelpNoLimit") : t("accessHelpBitrateMbps", { mbps: bps / 1_000_000 });
  return {
    presets: [
      { value: 0, label: t("accessHelpNoLimit") },
      { value: 2_000_000, label: t("accessHelpBitrate2") },
      { value: 4_000_000, label: t("accessHelpBitrate4") },
      { value: 8_000_000, label: t("accessHelpBitrate8") },
      { value: 20_000_000, label: t("accessHelpBitrate20") },
      { value: 40_000_000, label: t("accessHelpBitrate40") },
    ],
    format,
  };
}
