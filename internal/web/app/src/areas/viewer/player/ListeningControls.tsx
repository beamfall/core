import { ChevronRight, Gauge, Moon, Scissors } from "lucide-react";
import { type RefObject, useId } from "react";
import { Menu, Popover, Slider } from "@/components/ui/astryx-primitives";
import { Switch } from "@/components/ui/switch";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { formatClockTime } from "../lib/transport";
import {
  minutesAndSeconds,
  SLEEP_MINUTES,
  type SleepTimer,
  SPEED_MAX,
  SPEED_MIN,
  SPEED_PRESETS,
  SPEED_STEP,
} from "./listening";

export interface ListeningControlsProps {
  /** Where the popover and menu portal: the transport's layer, so they show in fullscreen. */
  container: RefObject<HTMLElement | null>;
  /** Which side of its row a popover opens on: left beside the sheet, or above it on a phone. */
  side?: "left" | "top";
  speed: number;
  /** Called while the slider moves; the rate follows without a write. */
  onSpeedPreview: (speed: number) => void;
  /** Called once a speed is chosen, to store it. */
  onSpeed: (speed: number) => void;
  /** The item's container Entity, named in "Use for …"; "" hides the toggle. */
  containerTitle: string;
  /** Whether the container's own speed is in effect. */
  forContainer: boolean;
  onForContainer: (on: boolean) => void;
  removePauses: boolean;
  onRemovePauses: (on: boolean) => void;
  /** Whether this item has a silence map with long pauses to remove. */
  pausesMapped: boolean;
  /** Seconds of pauses removed this session. */
  savedSeconds: number;
  sleep: SleepTimer | null;
  /** Seconds left on the sleep timer, or null. */
  sleepRemaining: number | null;
  /** Whether "end of this chapter" applies: the item has chapters. */
  canSleepAtChapter: boolean;
  onSleep: (choice: SleepChoice) => void;
}

export type SleepChoice = "off" | "item" | "chapter" | `${(typeof SLEEP_MINUTES)[number]}`;

// The audio stage's listening controls (CORE-2960): speed, removing long
// pauses and the sleep timer, drawn as rows of the transport's settings sheet.
// Every state is stored or held by the player; this only draws it.
export function ListeningControls(props: ListeningControlsProps) {
  const t = useT();
  const locale = useDisplayLocale();
  const number = new Intl.NumberFormat(locale, { maximumFractionDigits: 2 });
  const speedText = (value: number) => t("listenSpeedValue", { speed: number.format(value) });
  const pausesId = useId();
  const saved = minutesAndSeconds(props.savedSeconds);
  const pausesNote = !props.removePauses
    ? ""
    : props.pausesMapped
      ? t("listenSaved", saved)
      : t("listenNoPauseMap");

  return (
    <div className="bf-listen" role="group" aria-label={t("listenControls")}>
      <SpeedPopover {...props} speedText={speedText} />
      <label htmlFor={pausesId} className="bf-settings-row bf-listen-pauses">
        <span aria-hidden="true" className="bf-settings-tile">
          <Scissors />
        </span>
        <span className="bf-settings-label is-stacked">
          <span>{t("settingsTrimSilence")}</span>
          {pausesNote && (
            <span id={`${pausesId}-note`} className="bf-settings-sublabel">
              {pausesNote}
            </span>
          )}
        </span>
        <Switch
          id={pausesId}
          checked={props.removePauses}
          onCheckedChange={props.onRemovePauses}
          aria-describedby={pausesNote ? `${pausesId}-note` : undefined}
        />
      </label>
      <SleepMenu {...props} />
    </div>
  );
}

function SpeedPopover({
  container,
  side = "left",
  speed,
  onSpeedPreview,
  onSpeed,
  containerTitle,
  forContainer,
  onForContainer,
  speedText,
}: ListeningControlsProps & { speedText: (value: number) => string }) {
  const t = useT();
  const toggleId = useId();
  const current = speedText(speed);
  return (
    <Popover.Root>
      <Popover.Trigger
        className="bf-settings-row"
        aria-label={t("listenSpeedButton", { speed: current })}
      >
        <span aria-hidden="true" className="bf-settings-tile">
          <Gauge />
        </span>
        <span aria-hidden="true" className="bf-settings-label">
          {t("playbackSpeed")}
        </span>
        <span aria-hidden="true" className="bf-settings-value">
          {current}
        </span>
        <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
      </Popover.Trigger>
      <Popover.Portal container={container}>
        <Popover.Positioner
          className="bf-menu-positioner bf-listen-positioner"
          side={side}
          align={side === "top" ? "end" : "center"}
          sideOffset={10}
          collisionPadding={12}
        >
          <Popover.Popup className="bf-menu bf-listen-popup">
            <div className="bf-listen-popup-head">
              <Popover.Title className="bf-listen-popup-title">{t("listenSpeed")}</Popover.Title>
              <span aria-hidden="true" className="bf-listen-speed">
                {current}
              </span>
            </div>
            <Slider.Root
              min={SPEED_MIN}
              max={SPEED_MAX}
              step={SPEED_STEP}
              value={speed}
              onValueChange={(value) => onSpeedPreview(Number(value))}
              onValueCommitted={(value) => onSpeed(Number(value))}
              className="bf-slider-root bf-listen-slider"
            >
              <Slider.Control className="bf-slider-control">
                <Slider.Track className="bf-slider-track">
                  <Slider.Indicator className="bf-slider-indicator" />
                </Slider.Track>
                <Slider.Thumb
                  className="bf-slider-thumb"
                  getAriaLabel={() => t("playbackSpeed")}
                  getAriaValueText={(_, value) => speedText(value)}
                />
              </Slider.Control>
            </Slider.Root>
            <div className="bf-listen-presets" role="group" aria-label={t("listenSpeedPresets")}>
              {SPEED_PRESETS.map((preset) => (
                <button
                  key={preset}
                  type="button"
                  className="bf-listen-preset"
                  aria-pressed={Math.abs(speed - preset) < SPEED_STEP / 2}
                  onClick={() => onSpeed(preset)}
                >
                  {speedText(preset)}
                </button>
              ))}
            </div>
            {containerTitle && (
              <div className="bf-listen-toggle">
                <label htmlFor={toggleId}>
                  {t("listenSpeedForContainer", { title: containerTitle })}
                </label>
                <Switch id={toggleId} checked={forContainer} onCheckedChange={onForContainer} />
              </div>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

function SleepMenu({
  container,
  side = "left",
  sleep,
  sleepRemaining,
  canSleepAtChapter,
  onSleep,
}: ListeningControlsProps) {
  const t = useT();
  const value: SleepChoice = !sleep
    ? "off"
    : sleep.kind === "minutes"
      ? (String(sleep.minutes) as SleepChoice)
      : sleep.kind;
  const left = sleepRemaining === null ? "" : formatClockTime(Math.ceil(sleepRemaining));
  const item = (choice: SleepChoice, label: string) => (
    <Menu.RadioItem key={choice} value={choice} closeOnClick className="bf-menu-option">
      {label}
    </Menu.RadioItem>
  );
  return (
    <Menu.Root>
      <Menu.Trigger
        className="bf-settings-row"
        data-active={sleep ? "" : undefined}
        aria-label={left ? t("listenSleepLeft", { time: left }) : t("listenSleep")}
      >
        <span aria-hidden="true" className="bf-settings-tile">
          <Moon />
        </span>
        <span aria-hidden="true" className="bf-settings-label">
          {t("listenSleep")}
        </span>
        <span aria-hidden="true" className="bf-settings-value">
          {left || t("listenSleepOff")}
        </span>
        <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
      </Menu.Trigger>
      <Menu.Portal container={container}>
        <Menu.Positioner
          className="bf-menu-positioner bf-listen-positioner"
          side={side}
          align={side === "top" ? "end" : "center"}
          sideOffset={10}
          collisionPadding={12}
        >
          <Menu.Popup className="bf-menu bf-listen-menu">
            <Menu.RadioGroup
              value={value}
              onValueChange={(next: SleepChoice) => onSleep(next)}
              aria-label={t("listenSleep")}
            >
              {item("off", t("listenSleepOff"))}
              {SLEEP_MINUTES.map((minutes) =>
                item(String(minutes) as SleepChoice, t("listenSleepMinutes", { minutes })),
              )}
              {item("item", t("listenSleepItem"))}
              {(canSleepAtChapter || value === "chapter") &&
                item("chapter", t("listenSleepChapter"))}
            </Menu.RadioGroup>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
