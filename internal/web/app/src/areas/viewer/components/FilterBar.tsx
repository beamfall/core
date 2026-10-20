import { displayString } from "@/lib/display";
import type { FieldFilters, Section } from "@/lib/graphql/types";
import { useDisplayLocale } from "@/lib/i18n";

// Filter-bar Section, ported from the vanilla buildFilterBar (app.js). The
// manifest-declared options (Section.filters) render as toggle chips that narrow
// an adjacent Section by re-hydrating it with the selection merged into the
// Source's fieldFilters. One selection per field; clicking a pressed chip clears
// it. The selection state and the re-hydration target are owned by LayoutView;
// this component is the presentational chip row.
export function FilterBar({
  section,
  selected,
  onToggle,
}: {
  section: Section;
  /** field → selected value (one per field). */
  selected: FieldFilters;
  onToggle: (field: string, value: string) => void;
}) {
  const displayLocale = useDisplayLocale();
  const options = section.filters || [];
  if (!options.length) {
    return null;
  }
  return (
    <section className="bf-section flex flex-col gap-2">
      <h2 className="bf-eyebrow">{displayString(section.title, displayLocale)}</h2>
      <div className="bf-filter-chips">
        {options.map((option, index) => {
          const pressed = selected[option.field] === option.value;
          return (
            <button
              key={`${option.field}-${option.value}-${index}`}
              type="button"
              aria-pressed={pressed}
              onClick={() => onToggle(option.field, option.value)}
              className="bf-chip"
            >
              {displayString(option.label, displayLocale) || option.value}
            </button>
          );
        })}
      </div>
    </section>
  );
}
