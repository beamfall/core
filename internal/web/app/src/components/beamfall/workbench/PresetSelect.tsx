import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

export type Preset = { value: number; label: string };

// PresetSelect (CORE-2445): a number setting chosen from a few named values
// instead of typed. A saved value that is not one of the presets is still
// listed (by `format`, or its number), so the control never misreports what the
// server holds and choosing it again is a no-op.
export function presetOptions(
  value: number,
  presets: Preset[],
  format: (value: number) => string = String,
): Preset[] {
  if (presets.some((p) => p.value === value)) return presets;
  return [...presets, { value, label: format(value) }].sort((a, b) => a.value - b.value);
}

export function PresetSelect({
  id,
  value,
  presets,
  onChange,
  format,
  disabled,
  className,
  "aria-label": ariaLabel,
}: {
  id?: string;
  value: number;
  presets: Preset[];
  onChange: (value: number) => void;
  format?: (value: number) => string;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
}) {
  const options = presetOptions(value, presets, format);
  return (
    <Select
      value={String(value)}
      onValueChange={(next) => onChange(Number(next))}
      disabled={disabled}
    >
      <SelectTrigger id={id} aria-label={ariaLabel} className={cn("min-w-40", className)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={String(option.value)}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// A minute of the day (0–1440) as HH:MM; 1440 is the end of the day, "24:00".
export function formatMinuteOfDay(minute: number): string {
  const h = Math.floor(minute / 60);
  const m = minute % 60;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

// Every half hour of the day, plus 24:00 when `endOfDay` is set (an end time).
export function minuteOfDayPresets(endOfDay = false): Preset[] {
  const last = endOfDay ? 1440 : 1410;
  const presets: Preset[] = [];
  for (let minute = 0; minute <= last; minute += 30) {
    presets.push({ value: minute, label: formatMinuteOfDay(minute) });
  }
  return presets;
}

// TimeOfDaySelect: a time of day chosen in half-hour steps, stored as minutes
// since midnight. An off-step saved time is kept as its own choice.
export function TimeOfDaySelect({
  endOfDay,
  ...props
}: Omit<Parameters<typeof PresetSelect>[0], "presets" | "format"> & { endOfDay?: boolean }) {
  return (
    <PresetSelect {...props} presets={minuteOfDayPresets(endOfDay)} format={formatMinuteOfDay} />
  );
}
