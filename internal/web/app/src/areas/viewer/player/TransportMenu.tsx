import { ChevronRight } from "lucide-react";
import type { ReactNode, RefObject } from "react";
import { Select } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import type { TrackMenuOption } from "../lib/transport";

// A settings-sheet row that opens a Base UI listbox (audio / subtitle / quality).
// The row shows the current choice; the listbox portals into a layer inside the
// stage so it stays visible in stage fullscreen.
// An empty track set renders one disabled "Unavailable on this route" row.
export interface MenuRow<Id extends string = string> extends TrackMenuOption {
  id: Id;
}

export function TransportMenu({
  label,
  ariaLabel,
  options,
  open,
  onOpenChange,
  onSelect,
  icon,
  container,
  side = "left",
}: {
  /** The trigger row's visible text. */
  label: string;
  /** The settings-row icon tile. */
  icon?: ReactNode;
  /** Where the listbox portals: a layer inside the stage, so it shows in fullscreen. */
  container?: RefObject<HTMLElement | null>;
  side?: "left" | "top";
  /** The listbox's accessible name. */
  ariaLabel: string;
  options: MenuRow[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (id: string) => void;
}) {
  const t = useT();
  const rows: MenuRow[] = options.length
    ? options
    : [
        {
          id: "none",
          label: t("none"),
          selected: false,
          disabled: true,
          reason: t("trackUnavailable"),
        },
      ];
  const current = rows.find((row) => row.selected)?.label ?? rows[0]?.label ?? "";

  return (
    <Select.Root
      value={rows.find((row) => row.selected)?.id ?? null}
      open={open}
      onOpenChange={onOpenChange}
      onValueChange={(value) => {
        if (value === null) return;
        onSelect(String(value));
      }}
    >
      {/* `combobox` takes no name from content, so the trigger needs an
          explicit label; the listbox role lives on Select.List (Select.Popup
          degrades to `presentation` whenever a List is mounted inside it). */}
      <Select.Trigger aria-label={ariaLabel} className="bf-settings-row">
        <span aria-hidden="true" className="bf-settings-tile">
          {icon}
        </span>
        <span className="bf-settings-label">{label}</span>
        <span className="bf-settings-value">{current}</span>
        <ChevronRight aria-hidden="true" className="bf-settings-chevron" />
      </Select.Trigger>
      <Select.Portal container={container}>
        <Select.Positioner
          className="bf-menu-positioner"
          side={side}
          align={side === "top" ? "end" : "center"}
          alignItemWithTrigger={false}
          sideOffset={10}
          collisionPadding={12}
        >
          <Select.Popup className="bf-menu">
            <Select.List aria-label={ariaLabel} className="bf-menu-list">
              {rows.map((row) => (
                <Select.Item
                  key={row.id}
                  value={row.id}
                  disabled={row.disabled}
                  nativeButton
                  render={<button type="button" />}
                  className="bf-menu-option"
                >
                  <Select.ItemText>{row.label}</Select.ItemText>
                  {row.reason && <span className="bf-menu-reason">{row.reason}</span>}
                </Select.Item>
              ))}
            </Select.List>
          </Select.Popup>
        </Select.Positioner>
      </Select.Portal>
    </Select.Root>
  );
}
