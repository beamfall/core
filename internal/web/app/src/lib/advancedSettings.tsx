import { type ReactNode, useId, useState, useSyncExternalStore } from "react";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

// The one Normal/Advanced view preference for settings surfaces (CORE-1117, OpenSpec
// web-advanced-settings). Normal is the default and hides expert destinations and controls;
// Advanced shows everything. It is per browser: localStorage when the browser allows it, and an
// in-memory value for the rest of the session when it does not.
export const ADVANCED_SETTINGS_KEY = "beamfall.settings.advanced";

const listeners = new Set<() => void>();
// Set only when storage refused a write, so the switch still works for this session.
let sessionValue: boolean | undefined;

function getSnapshot(): boolean {
  if (sessionValue !== undefined) return sessionValue;
  try {
    return globalThis.localStorage?.getItem(ADVANCED_SETTINGS_KEY) === "1";
  } catch {
    return false;
  }
}

function getServerSnapshot(): boolean {
  return false;
}

function onStorage(event: StorageEvent) {
  if (event.key !== null && event.key !== ADVANCED_SETTINGS_KEY) return;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) globalThis.addEventListener?.("storage", onStorage);
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) globalThis.removeEventListener?.("storage", onStorage);
  };
}

export function setAdvancedSettings(on: boolean) {
  try {
    globalThis.localStorage.setItem(ADVANCED_SETTINGS_KEY, on ? "1" : "0");
    sessionValue = undefined;
  } catch {
    sessionValue = on;
  }
  for (const listener of listeners) listener();
}

export function useAdvancedSettings(): [boolean, (on: boolean) => void] {
  return [useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot), setAdvancedSettings];
}

// Whether an Advanced control shows. `nonDefault` keeps it visible in Normal while its value is not
// the default; once kept it stays for the life of the component, so editing it back to the
// default does not make it vanish mid-edit.
export function useShowAdvanced(nonDefault = false): boolean {
  const [advanced] = useAdvancedSettings();
  const [kept, setKept] = useState(nonDefault);
  if (nonDefault && !kept) setKept(true);
  return advanced || nonDefault || kept;
}

export function Advanced({ keep = false, children }: { keep?: boolean; children: ReactNode }) {
  return useShowAdvanced(keep) ? children : null;
}

export function AdvancedSettingsSwitch({ className }: { className?: string }) {
  const t = useT();
  const id = useId();
  const [advanced, setAdvanced] = useAdvancedSettings();
  return (
    <div className={cn("flex min-h-8 items-center justify-between gap-3", className)}>
      <label htmlFor={id} className="cursor-pointer text-dense text-muted-foreground">
        {t("showAdvancedSettings")}
      </label>
      <Switch id={id} checked={advanced} onCheckedChange={setAdvanced} />
    </div>
  );
}
