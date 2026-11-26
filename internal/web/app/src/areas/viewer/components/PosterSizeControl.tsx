import { POSTER_SIZES, type PosterSize, usePosterSize } from "@/components/chrome/posterSize";
import { ActionTooltip } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";

const SIZE_LABELS: Record<PosterSize, { name: StringKey; short: StringKey }> = {
  small: { name: "libraryPosterSizeSmall", short: "libraryPosterSizeSmallShort" },
  medium: { name: "libraryPosterSizeMedium", short: "libraryPosterSizeMediumShort" },
  large: { name: "libraryPosterSizeLarge", short: "libraryPosterSizeLargeShort" },
};

/**
 * Small / medium / large chips for the poster grids (CORE-2468). The choice
 * is one per browser and reaches every viewer grid, not only this Library.
 */
export function PosterSizeControl({ className = "" }: { className?: string }) {
  const t = useT();
  const [size, setSize] = usePosterSize();
  return (
    <div
      role="group"
      aria-label={t("libraryPosterSizeLabel")}
      className={`flex items-center gap-1 ${className}`}
    >
      {POSTER_SIZES.map((option) => {
        const label = t(SIZE_LABELS[option].name);
        return (
          <ActionTooltip key={option} label={label}>
            <button
              type="button"
              className="bf-chip bf-chip-square"
              aria-label={label}
              aria-pressed={size === option}
              onClick={() => setSize(option)}
            >
              {t(SIZE_LABELS[option].short)}
            </button>
          </ActionTooltip>
        );
      })}
    </div>
  );
}
