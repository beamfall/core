import { ArrowDown, ArrowUp, Plus, X } from "lucide-react";
import { type FormEvent, useEffect, useRef, useState } from "react";
import { FolderPicker } from "@/areas/setup/FolderPicker";
import { ControlRow, ErrorState, Fieldset, PresetSelect } from "@/components/beamfall/workbench";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Advanced } from "@/lib/advancedSettings";
import {
  browseServerFolders,
  fetchConsoleConfig,
  fetchPrePlayClips,
  fetchTranscodeConfig,
  MAX_PRE_PLAY_CLIPS,
  search,
  setConsoleConfig,
  setPrePlayClips,
  setTranscodeConfig,
} from "@/lib/graphql";
import type {
  FolderEntryInfo,
  PrePlayItem,
  SourceItem,
  TranscodeConfigInfo,
} from "@/lib/graphql/types";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import { Section } from "../Section";
import { useAsync } from "../useAsync";

type ChoiceField = "codec" | "quality";

// The server reads "" as its own default; a Select cannot hold "", so the
// default choice carries this sentinel and is mapped back on change.
const SERVER_DEFAULT = "__default";

const CHOICE_FIELDS: {
  field: ChoiceField;
  label: StringKey;
  tip: StringKey;
  options: { value: string; label: StringKey }[];
}[] = [
  {
    field: "codec",
    label: "transcodeCodec",
    tip: "transcodeCodecTip",
    options: [
      { value: "h264", label: "transcodeCodecH264" },
      { value: "hevc", label: "transcodeCodecHevc" },
    ],
  },
  {
    field: "quality",
    label: "transcodeQuality",
    tip: "transcodeQualityTip",
    options: [
      { value: "low", label: "transcodeQualityLow" },
      { value: "balanced", label: "transcodeQualityBalanced" },
      { value: "high", label: "transcodeQualityHigh" },
    ],
  },
];

// Simultaneous-transcode choices; 0 is automatic (the server sizes it).
const CAP_PRESETS = [0, 1, 2, 3, 4, 6, 8];

// Hardware transcoding policy (ADR-0007 §6 / ADR-0010). Empty string values
// fall back to the server default server-side. The on/off switch is the Normal
// control; codec, quality, device and the probe list are Advanced, and a field
// that holds a value, saved or being edited, stays visible in Normal so Save
// never submits a setting out of sight (CORE-1117).
//
// Resource settings (CORE-1970): the concurrent-transcode cap (automatic by
// default) and the HDR tone-map switch are Normal and apply live; the temp
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
  const [cap, setCap] = useState(0);
  const [saveError, setSaveError] = useState("");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  // The temp directory's folder browse: the directory on show, or null when closed.
  const [pickerNode, setPickerNode] = useState<FolderEntryInfo | null>(null);
  const [pickerBusy, setPickerBusy] = useState(false);
  const [pickerError, setPickerError] = useState<string | null>(null);
  const browseRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (data) {
      setForm(data);
      setCap(Math.max(0, data.maxConcurrentTranscodes));
    }
  }, [data]);

  // "Saved" stays up only while the form still matches what the server holds.
  const unchanged =
    data != null &&
    cap === Math.max(0, data.maxConcurrentTranscodes) &&
    form.hardwareEnabled === data.hardwareEnabled &&
    form.toneMapEnabled === data.toneMapEnabled &&
    form.codec === data.codec &&
    form.quality === data.quality &&
    form.device === data.device &&
    form.tempDir.trim() === data.tempDir;

  async function loadPickerFolder(path?: string): Promise<boolean> {
    setPickerBusy(true);
    setPickerError(null);
    try {
      setPickerNode(await browseServerFolders(path));
      return true;
    } catch {
      setPickerError(t("setupLibraryPickerError"));
      return false;
    } finally {
      setPickerBusy(false);
    }
  }

  // Opens at the typed directory, else the one in use; a directory the server
  // cannot open falls back to the filesystem root with a note.
  async function openPicker() {
    const seed = form.tempDir.trim() || data?.effectiveTempDir || "";
    if ((await loadPickerFolder(seed || undefined)) || !seed) return;
    if (await loadPickerFolder(undefined)) {
      setPickerError(t("setupLibraryPickerSeedError", { path: seed }));
    }
  }

  function closePicker() {
    setPickerNode(null);
    setPickerError(null);
    requestAnimationFrame(() => browseRef.current?.focus());
  }

  async function onSave(e: FormEvent) {
    e.preventDefault();
    setSaveError("");
    setSaved(false);
    setSaving(true);
    try {
      const next = await setTranscodeConfig({
        hardwareEnabled: form.hardwareEnabled,
        codec: form.codec,
        quality: form.quality,
        device: form.device,
        maxConcurrentTranscodes: cap > 0 ? cap : null,
        tempDir: form.tempDir.trim(),
        toneMapEnabled: form.toneMapEnabled,
      });
      setData(next);
      setSaved(true);
    } catch (err) {
      setSaveError(t("transcodeConfigSaveFailed", { reason: errorReason(err) }));
    } finally {
      setSaving(false);
    }
  }

  if (failed) return <ErrorState message="transcodeConfigUnavailable" />;

  return (
    <form onSubmit={onSave} className="space-y-3">
      <Fieldset title="transcodeConfigHeading" help="transcodeConfigLead">
        <ControlRow
          label="transcodeHardwareEnabled"
          tip="transcodeHardwareEnabledTip"
          setting="console.playback.hardware"
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
          tip="transcodeMaxConcurrentTip"
          setting="console.playback.maxTranscodes"
          control={
            <PresetSelect
              id="tc-max-concurrent"
              aria-label={t("transcodeMaxConcurrent")}
              value={cap}
              presets={CAP_PRESETS.map((value) => ({
                value,
                label: value === 0 ? t("transcodeAutomatic") : String(value),
              }))}
              onChange={setCap}
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
          setting="console.playback.toneMap"
          control={
            <Switch
              aria-label={t("transcodeToneMapEnabled")}
              checked={form.toneMapEnabled}
              onCheckedChange={(c) => setForm((f) => ({ ...f, toneMapEnabled: c }))}
            />
          }
        />
        {CHOICE_FIELDS.map(({ field, label, tip, options }) => (
          <Advanced key={field} keep={Boolean(form[field])}>
            <ControlRow
              label={label}
              tip={tip}
              setting={`console.playback.${field}`}
              control={
                <Select
                  value={form[field] || SERVER_DEFAULT}
                  onValueChange={(next) =>
                    setForm((f) => ({ ...f, [field]: next === SERVER_DEFAULT ? "" : next }))
                  }
                >
                  <SelectTrigger id={`tc-${field}`} aria-label={t(label)} className="min-w-44">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value={SERVER_DEFAULT}>{t("transcodeServerDefault")}</SelectItem>
                    {options.map((option) => (
                      <SelectItem key={option.value} value={option.value}>
                        {t(option.label)}
                      </SelectItem>
                    ))}
                    {form[field] && !options.some((o) => o.value === form[field]) ? (
                      <SelectItem value={form[field]}>{form[field]}</SelectItem>
                    ) : null}
                  </SelectContent>
                </Select>
              }
            />
          </Advanced>
        ))}
        <Advanced keep={Boolean(form.device)}>
          <ControlRow
            label="transcodeDevice"
            tip="transcodeDeviceTip"
            setting="console.playback.device"
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
        </Advanced>
        <Advanced
          keep={
            Boolean(form.tempDir) ||
            Boolean(data?.tempDirEnvOverride || data?.tempDirRestartRequired)
          }
        >
          <ControlRow
            label="transcodeTempDir"
            tip="transcodeTempDirTip"
            setting="console.playback.tempDir"
            control={
              <div className="flex items-center gap-2">
                <Input
                  id="tc-temp-dir"
                  aria-label={t("transcodeTempDir")}
                  value={form.tempDir}
                  placeholder={t("transcodeServerDefault")}
                  className="w-64 font-mono"
                  autoComplete="off"
                  spellCheck={false}
                  onChange={(e) => setForm((f) => ({ ...f, tempDir: e.target.value }))}
                />
                <Button
                  ref={browseRef}
                  type="button"
                  variant="outline"
                  aria-expanded={pickerNode != null}
                  onClick={() => (pickerNode ? closePicker() : void openPicker())}
                >
                  {t("setupLibraryBrowse")}
                </Button>
              </div>
            }
          />
          {pickerNode == null && pickerError ? (
            <p role="alert" className="text-sm text-destructive">
              {pickerError}
            </p>
          ) : null}
          {pickerNode != null ? (
            <FolderPicker
              node={pickerNode}
              busy={pickerBusy}
              error={pickerError}
              onOpenFolder={(path) => void loadPickerFolder(path)}
              onUse={() => {
                setForm((f) => ({ ...f, tempDir: pickerNode.path }));
                closePicker();
              }}
              onClose={closePicker}
              labels={{
                use: t("setupLibraryPickerUse"),
                loading: t("setupLibraryPickerLoading"),
                empty: t("setupLibraryPickerEmpty"),
                close: t("setupLibraryPickerClose"),
              }}
            />
          ) : null}
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
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={saving}>
          {t("transcodeConfigSave")}
        </Button>
        <span role="status" className="text-sm text-muted-foreground">
          {saved && unchanged ? t("transcodeConfigSaved") : null}
        </span>
      </div>
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
        setting="console.config.pairingCodes"
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

const SELECT_CLASS =
  "h-10 rounded-[var(--radius-control)] border border-input bg-transparent px-3 text-sm";

// 0 is the server's "never expires" value for both lifetimes.
const SESSION_DAY_CHOICES = [7, 30, 90, 365, 0];
const ADMIN_MINUTE_CHOICES = [15, 60, 480, 1440, 10080, 0];

// The saved value always appears as a choice, so a lifetime set through the
// API to something off the list is shown instead of being silently replaced.
function withSaved(choices: number[], saved: number): number[] {
  if (choices.includes(saved)) return choices;
  const timed = [...choices.filter((choice) => choice !== 0), saved].sort((a, b) => a - b);
  return [...timed, 0];
}

// Session lifetimes (CORE-2303): how long a device stays signed in and how
// long the Console stays unlocked. Saved on change; both offer Never expire.
function SessionsConfig() {
  const t = useT();
  const { data, failed, setData } = useAsync(fetchConsoleConfig);
  const [saveFailed, setSaveFailed] = useState(false);

  async function save(patch: { sessionLifetimeDays?: number; adminSessionMinutes?: number }) {
    setSaveFailed(false);
    try {
      setData(await setConsoleConfig(patch));
    } catch {
      setSaveFailed(true);
    }
  }

  function minutesLabel(minutes: number): string {
    if (minutes % 1440 === 0) return t("sessionLengthDays", { count: minutes / 1440 });
    if (minutes % 60 === 0) return t("sessionLengthHours", { count: minutes / 60 });
    return t("sessionLengthMinutes", { count: minutes });
  }

  if (failed) return <ErrorState message="consoleConfigUnavailable" />;

  const days = data?.sessionLifetimeDays ?? 30;
  const minutes = data?.adminSessionMinutes ?? 15;

  return (
    <Fieldset title="sessionsHeading">
      <ControlRow
        label="sessionLifetimeLabel"
        setting="console.config.sessionLifetime"
        help="sessionLifetimeHelp"
        control={
          <select
            aria-label={t("sessionLifetimeLabel")}
            className={SELECT_CLASS}
            value={days}
            disabled={!data}
            onChange={(event) => void save({ sessionLifetimeDays: Number(event.target.value) })}
          >
            {withSaved(SESSION_DAY_CHOICES, days).map((choice) => (
              <option key={choice} value={choice}>
                {choice === 0 ? t("sessionNeverExpire") : t("sessionLengthDays", { count: choice })}
              </option>
            ))}
          </select>
        }
      />
      <ControlRow
        label="adminSessionLengthLabel"
        setting="console.config.adminSession"
        help="adminSessionLengthHelp"
        control={
          <select
            aria-label={t("adminSessionLengthLabel")}
            className={SELECT_CLASS}
            value={minutes}
            disabled={!data}
            onChange={(event) => void save({ adminSessionMinutes: Number(event.target.value) })}
          >
            {withSaved(ADMIN_MINUTE_CHOICES, minutes).map((choice) => (
              <option key={choice} value={choice}>
                {choice === 0 ? t("sessionNeverExpire") : minutesLabel(choice)}
              </option>
            ))}
          </select>
        }
      />
      <p className="text-sm text-muted-foreground">{t("sessionNeverExpireWarning")}</p>
      {saveFailed ? (
        <p role="alert" className="text-sm text-destructive">
          {t("couldNotSaveSessionLength")}
        </p>
      ) : null}
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
    <Section heading="navPlaybackSignIn" lead="configPageLead">
      <div className="space-y-6">
        <ViewerAccessConfig />
        <SessionsConfig />
        <TranscodeConfig />
        <PrePlayClipsConfig />
      </div>
    </Section>
  );
}
