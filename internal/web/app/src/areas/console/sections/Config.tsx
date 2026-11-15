import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { ControlRow, ErrorState, Fieldset } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Advanced } from "@/lib/advancedSettings";
import {
  fetchConsoleConfig,
  fetchPrePlayClips,
  fetchTranscodeConfig,
  MAX_PRE_PLAY_CLIPS,
  search,
  setConsoleConfig,
  setPrePlayClips,
  setTranscodeConfig,
} from "@/lib/graphql";
import type { PrePlayItem, SourceItem, TranscodeConfigInfo } from "@/lib/graphql/types";
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
//
// Resource settings (CORE-1970): the concurrent-transcode cap (empty means
// automatic) and the HDR tone-map switch are Normal and apply live; the temp
// directory is Advanced and needs a restart. Each shows the value in effect and
// says when an environment variable overrides the saved one.
const EMPTY_TRANSCODE_CONFIG: TranscodeConfigInfo = {
  hardwareEnabled: false,
  codec: "",
  quality: "",
  device: "",
  configuredPreference: "",
  hardwareAcceleration: [],
  maxConcurrentTranscodes: 0,
  effectiveMaxConcurrentTranscodes: 0,
  maxConcurrentTranscodesEnvOverride: false,
  maxConcurrentTranscodesRestartRequired: false,
  tempDir: "",
  effectiveTempDir: "",
  tempDirEnvOverride: false,
  tempDirRestartRequired: false,
  toneMapEnabled: true,
};

function capText(cap: number): string {
  return cap > 0 ? String(cap) : "";
}

// parseCap returns null for automatic (empty), a whole number of at least 1,
// or undefined when the text is not a valid cap.
function parseCap(text: string): number | null | undefined {
  const trimmed = text.trim();
  if (trimmed === "") return null;
  if (!/^\d+$/.test(trimmed)) return undefined;
  const n = Number(trimmed);
  return n >= 1 ? n : undefined;
}

function errorReason(err: unknown): string {
  return err instanceof Error ? err.message : "";
}

function StatusNote({ children }: { children: string }) {
  return <p className="console-control-help">{children}</p>;
}

function TranscodeConfig() {
  const t = useT();
  const { data, failed, setData } = useAsync(fetchTranscodeConfig);
  const [form, setForm] = useState<TranscodeConfigInfo>(EMPTY_TRANSCODE_CONFIG);
  const [cap, setCap] = useState("");
  const [saveError, setSaveError] = useState("");

  useEffect(() => {
    if (data) {
      setForm(data);
      setCap(capText(data.maxConcurrentTranscodes));
    }
  }, [data]);

  const parsedCap = parseCap(cap);

  async function onSave(e: FormEvent) {
    e.preventDefault();
    if (parsedCap === undefined) {
      setSaveError(t("transcodeMaxConcurrentInvalid"));
      return;
    }
    setSaveError("");
    try {
      const next = await setTranscodeConfig({
        hardwareEnabled: form.hardwareEnabled,
        codec: form.codec,
        quality: form.quality,
        device: form.device,
        maxConcurrentTranscodes: parsedCap,
        tempDir: form.tempDir.trim(),
        toneMapEnabled: form.toneMapEnabled,
      });
      setData(next);
    } catch (err) {
      setSaveError(t("transcodeConfigSaveFailed", { reason: errorReason(err) }));
    }
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
        <ControlRow
          label="transcodeMaxConcurrent"
          control={
            <Input
              id="tc-max-concurrent"
              aria-label={t("transcodeMaxConcurrent")}
              inputMode="numeric"
              value={cap}
              placeholder={t("transcodeAutomatic")}
              aria-invalid={parsedCap === undefined}
              className="w-28"
              onChange={(e) => setCap(e.target.value)}
            />
          }
        />
        {data ? (
          <div data-testid="transcode-cap-status">
            <StatusNote>
              {t("transcodeMaxConcurrentEffective", {
                count: String(data.effectiveMaxConcurrentTranscodes),
              })}
            </StatusNote>
            {data.maxConcurrentTranscodesEnvOverride ? (
              <StatusNote>{t("transcodeMaxConcurrentEnvOverride")}</StatusNote>
            ) : null}
            {data.maxConcurrentTranscodesRestartRequired ? (
              <StatusNote>{t("transcodeRestartRequired")}</StatusNote>
            ) : null}
          </div>
        ) : null}
        <ControlRow
          label="transcodeToneMapEnabled"
          help="transcodeToneMapHelp"
          control={
            <Switch
              aria-label={t("transcodeToneMapEnabled")}
              checked={form.toneMapEnabled}
              onCheckedChange={(c) => setForm((f) => ({ ...f, toneMapEnabled: c }))}
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
        <Advanced
          keep={
            Boolean(form.tempDir) ||
            Boolean(data?.tempDirEnvOverride || data?.tempDirRestartRequired)
          }
        >
          <ControlRow
            label="transcodeTempDir"
            control={
              <Input
                id="tc-temp-dir"
                aria-label={t("transcodeTempDir")}
                value={form.tempDir}
                placeholder={t("transcodeServerDefault")}
                className="w-64"
                onChange={(e) => setForm((f) => ({ ...f, tempDir: e.target.value }))}
              />
            }
          />
          {data ? (
            <div data-testid="transcode-temp-dir-status">
              <StatusNote>
                {t("transcodeTempDirEffective", { dir: data.effectiveTempDir })}
              </StatusNote>
              {data.tempDirEnvOverride ? (
                <StatusNote>{t("transcodeTempDirEnvOverride")}</StatusNote>
              ) : null}
              {data.tempDirRestartRequired ? (
                <StatusNote>{t("transcodeRestartRequired")}</StatusNote>
              ) : null}
            </div>
          ) : null}
        </Advanced>
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
      {saveError ? (
        <p role="alert" className="text-sm text-destructive">
          {saveError}
        </p>
      ) : null}
      <Button type="submit">{t("transcodeConfigSave")}</Button>
    </form>
  );
}

// The Viewer pairing-code switch (CORE-0393), a Normal control saved on toggle.
// The section heading already names the group, so the legend is for assistive
// tech only.
function ViewerAccessConfig() {
  const t = useT();
  const { data, failed, setData } = useAsync(fetchConsoleConfig);

  async function onToggle(disabled: boolean) {
    const next = await setConsoleConfig({ viewerPairingCodesDisabled: disabled });
    setData(next);
  }

  if (failed) return <ErrorState message="consoleConfigUnavailable" />;

  return (
    <Fieldset title="consoleConfigHeading" hideTitle>
      <ControlRow
        label="viewerPairingCodesDisabled"
        help="viewerPairingCodesDisabledHelp"
        control={
          <Switch
            aria-label={t("viewerPairingCodesDisabled")}
            checked={data?.viewerPairingCodesDisabled ?? false}
            disabled={!data}
            onCheckedChange={(v) => void onToggle(v)}
          />
        }
      />
    </Fieldset>
  );
}

type Clip = Pick<PrePlayItem, "mediaID" | "title">;

// The Owner's pre-play clips (CORE-1701): local intro clips that play, in this
// order, before an eligible title for Profiles that opt in. Core only keeps the
// list; an extension decides which titles are eligible. Edits stay local until
// Save, which replaces the whole list.
function PrePlayClipsConfig() {
  const t = useT();
  const { data, failed, setData } = useAsync(fetchPrePlayClips);
  const [clips, setClips] = useState<Clip[]>([]);
  const [term, setTerm] = useState("");
  const [results, setResults] = useState<SourceItem[]>([]);
  const [saved, setSaved] = useState(false);
  const [saveFailed, setSaveFailed] = useState(false);

  useEffect(() => {
    if (data) setClips(data.items.map(({ mediaID, title }) => ({ mediaID, title })));
  }, [data]);

  function edit(next: Clip[]) {
    setClips(next);
    setSaved(false);
  }

  function move(index: number, by: -1 | 1) {
    const next = [...clips];
    const [clip] = next.splice(index, 1);
    next.splice(index + by, 0, clip);
    edit(next);
  }

  async function onSearch(e: FormEvent) {
    e.preventDefault();
    const query = term.trim();
    setResults(query ? await search(query).catch(() => []) : []);
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    try {
      const next = await setPrePlayClips(clips.map((clip) => clip.mediaID));
      setData(next);
      setSaveFailed(false);
      setSaved(true);
    } catch {
      setSaveFailed(true);
    }
  }

  if (failed) return <ErrorState message="prePlayClipsUnavailable" />;

  const chosen = new Set(clips.map((clip) => clip.mediaID));
  const full = clips.length >= MAX_PRE_PLAY_CLIPS;

  return (
    <div className="space-y-3">
      <Fieldset title="prePlayClipsHeading">
        <p className="console-fieldset-help">
          {t("prePlayClipsLead", { max: MAX_PRE_PLAY_CLIPS })}
        </p>
        {clips.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("prePlayClipsEmpty")}</p>
        ) : (
          <ol className="grid gap-2 text-sm">
            {clips.map((clip, index) => (
              <li
                key={clip.mediaID}
                className="flex items-center justify-between gap-3 rounded border px-3 py-2"
              >
                <span className="min-w-0 break-words">{clip.title}</span>
                <span className="flex shrink-0 gap-1">
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    disabled={index === 0}
                    aria-label={t("prePlayClipsUp", { title: clip.title })}
                    onClick={() => move(index, -1)}
                  >
                    <ArrowUp aria-hidden="true" />
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    disabled={index === clips.length - 1}
                    aria-label={t("prePlayClipsDown", { title: clip.title })}
                    onClick={() => move(index, 1)}
                  >
                    <ArrowDown aria-hidden="true" />
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    aria-label={t("prePlayClipsRemove", { title: clip.title })}
                    onClick={() => edit(clips.filter((c) => c.mediaID !== clip.mediaID))}
                  >
                    <X aria-hidden="true" />
                  </Button>
                </span>
              </li>
            ))}
          </ol>
        )}
        <form onSubmit={onSearch} className="flex gap-2">
          <Input
            type="search"
            aria-label={t("prePlayClipsSearch")}
            placeholder={t("prePlayClipsSearch")}
            value={term}
            onChange={(e) => setTerm(e.target.value)}
          />
        </form>
        {results.length > 0 ? (
          <ul className="grid gap-2 text-sm">
            {results
              .filter((item) => !chosen.has(item.id))
              .map((item) => (
                <li key={item.id} className="flex items-center justify-between gap-3">
                  <span className="min-w-0 break-words">{item.title}</span>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon-sm"
                    disabled={full}
                    aria-label={t("prePlayClipsAdd", { title: item.title })}
                    onClick={() => edit([...clips, { mediaID: item.id, title: item.title }])}
                  >
                    <Plus aria-hidden="true" />
                  </Button>
                </li>
              ))}
          </ul>
        ) : null}
      </Fieldset>
      <form onSubmit={onSave} className="flex items-center gap-3">
        <Button type="submit" disabled={!data}>
          {t("prePlayClipsSave")}
        </Button>
        <span role="status" className="text-sm text-muted-foreground">
          {saved ? t("prePlayClipsSaved") : saveFailed ? t("prePlayClipsSaveFailed") : null}
        </span>
      </form>
    </div>
  );
}

// Server configuration (ADR-0007 §4). Bootstrap config (ports, data dir,
// secrets) is deliberately not editable here, and the background-worker pool
// lives on Operations alone.
export function Config() {
  return (
    <Section heading="consoleConfigHeading" lead="consoleConfigLead">
      <div className="space-y-6">
        <ViewerAccessConfig />
        <TranscodeConfig />
        <PrePlayClipsConfig />
      </div>
    </Section>
  );
}
