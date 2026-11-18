import { GalleryHorizontal, LayoutGrid, List } from "lucide-react";
import { useCallback, useState } from "react";
import { useT } from "@/lib/i18n";
import type { StringKey } from "@/strings";
import {
  LIBRARY_VIEW_MODES,
  type LibraryViewMode,
  readLibraryViewMode,
  writeLibraryViewMode,
} from "../lib/libraryBrowse";

const MODE_LABELS: Record<LibraryViewMode, StringKey> = {
  poster: "libraryBrowseViewPoster",
  thumbnail: "libraryBrowseViewThumbnail",
  list: "libraryBrowseViewList",
};

const MODE_ICONS = { poster: LayoutGrid, thumbnail: GalleryHorizontal, list: List } as const;

/**
 * The display a Library's items are drawn in (CORE-2386), remembered per
 * Library in this browser and shared by its all-titles and folder views.
 */
export function useLibraryViewMode(
  libraryID: string,
): [LibraryViewMode, (mode: LibraryViewMode) => void] {
  // The choice is keyed by Library so moving to another Library reads its own.
  const [chosen, setChosen] = useState(() => ({
    libraryID,
    mode: readLibraryViewMode(libraryID),
  }));
  const mode = chosen.libraryID === libraryID ? chosen.mode : readLibraryViewMode(libraryID);
  const choose = useCallback(
    (next: LibraryViewMode) => {
      setChosen({ libraryID, mode: next });
      writeLibraryViewMode(libraryID, next);
    },
    [libraryID],
  );
  return [mode, choose];
}

/** The poster / thumbnail / list chips for a Library's items. */
export function LibraryDisplayToggle({
  mode,
  onChange,
  className = "",
}: {
  mode: LibraryViewMode;
  onChange: (mode: LibraryViewMode) => void;
  className?: string;
}) {
  const t = useT();
  return (
    <div
      role="group"
      aria-label={t("libraryBrowseViewLabel")}
      className={`flex flex-wrap items-center gap-2 ${className}`}
    >
      {LIBRARY_VIEW_MODES.map((option) => {
        const Icon = MODE_ICONS[option];
        return (
          <button
            key={option}
            type="button"
            className="bf-chip"
            aria-pressed={mode === option}
            onClick={() => onChange(option)}
          >
            <Icon aria-hidden className="size-4" />
            {t(MODE_LABELS[option])}
          </button>
        );
      })}
    </div>
  );
}
