import { Clapperboard, Compass, Headphones, Image, Sparkles } from "lucide-react";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";
import { type ExperienceMode, experienceModeLabel, useExperienceLens } from "./experienceLens";

export function modeIconFor(id: string) {
  switch (id) {
    case "all":
      return Compass;
    case "watch":
      return Clapperboard;
    case "listen":
      return Headphones;
    case "image":
      return Image;
    default:
      return Sparkles;
  }
}

export function ModeLensPill({
  variant = "sidebar",
  collapsed = false,
  className,
  onSelectMode,
}: {
  variant?: "sidebar" | "inline";
  collapsed?: boolean;
  className?: string;
  onSelectMode?: (mode: ExperienceMode["id"]) => void;
}) {
  const t = useT();
  const { mode, modes, showModePill, setMode } = useExperienceLens();

  if (!showModePill) return null;

  const isSidebarRail = variant === "sidebar" && collapsed;

  return (
    <div
      role="group"
      aria-label={t("lensModeLabel")}
      className={cn(
        "bf-lens-pill",
        variant === "inline" ? "is-inline" : "is-sidebar",
        isSidebarRail && "is-rail",
        className,
      )}
    >
      {modes.map((entry) => {
        const active = entry.id === mode;
        const Icon = modeIconFor(entry.id);
        const label = experienceModeLabel(entry, t);
        return (
          <button
            key={entry.id}
            type="button"
            aria-pressed={active}
            aria-label={label}
            title={isSidebarRail ? label : undefined}
            className={cn("bf-lens-segment", active && "is-active")}
            onClick={() => {
              setMode(entry.id);
              onSelectMode?.(entry.id);
            }}
          >
            <Icon aria-hidden className="bf-lens-icon size-3.5 shrink-0" />
            {!isSidebarRail && <span className="bf-lens-text">{label}</span>}
          </button>
        );
      })}
    </div>
  );
}
