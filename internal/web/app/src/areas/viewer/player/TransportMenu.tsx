import { type ComponentPropsWithRef, useCallback, useEffect, useState } from "react";
import { Select } from "@/components/ui/astryx-primitives";
import { useT } from "@/lib/i18n";
import type { TrackMenuOption } from "../lib/transport";

// A Base UI listbox floating above its trigger (audio / subtitle / quality).
// An empty track set renders one disabled "Unavailable on this route" row.
export interface MenuRow<Id extends string = string> extends TrackMenuOption {
  id: Id;
}

function InteractiveRow({
  highlighted,
  rowID,
  onHighlightedChange,
  ...props
}: ComponentPropsWithRef<"button"> & {
  highlighted: boolean;
  rowID: string;
  onHighlightedChange: (rowID: string, highlighted: boolean) => void;
}) {
  useEffect(() => {
    onHighlightedChange(rowID, highlighted);
  }, [highlighted, onHighlightedChange, rowID]);

  return <button {...props} />;
}

export function TransportMenu({
  label,
  ariaLabel,
  options,
  open,
  onOpenChange,
  onSelect,
}: {
  /** The trigger button's visible text. */
  label: string;
  /** The listbox's accessible name. */
  ariaLabel: string;
  options: MenuRow[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (id: string) => void;
}) {
  const t = useT();
  const [highlightedID, setHighlightedID] = useState<string | null>(null);
  const mirrorHighlight = useCallback((rowID: string, highlighted: boolean) => {
    setHighlightedID((current) => {
      if (highlighted) return rowID;
      return current === rowID ? null : current;
    });
  }, []);
  useEffect(() => {
    if (!open) setHighlightedID(null);
  }, [open]);
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
      <div className="bf-menu-anchor">
        {/* `combobox` takes no name from content, so the trigger needs an
            explicit label; the listbox role lives on Select.List (Select.Popup
            degrades to `presentation` whenever a List is mounted inside it). */}
        <Select.Trigger aria-label={ariaLabel} className="bf-control">
          {label}
        </Select.Trigger>
        {open && (
          <div aria-hidden="true" className="bf-menu bf-menu-visual">
            {rows.map((row) => (
              <button
                key={row.id}
                type="button"
                tabIndex={-1}
                aria-selected={row.selected}
                disabled={row.disabled}
                data-highlighted={row.id === highlightedID ? "" : undefined}
                className={`bf-menu-option${row.id === highlightedID ? " is-focused" : ""}`}
              >
                {row.label}
                {row.reason && <span className="bf-menu-reason">{row.reason}</span>}
              </button>
            ))}
          </div>
        )}
      </div>
      <Select.Portal>
        <Select.Positioner side="top" align="end" alignItemWithTrigger={false} sideOffset={8}>
          <Select.Popup className="bf-menu bf-menu-interactive">
            <Select.List aria-label={ariaLabel} className="bf-menu-list">
              {rows.map((row) => (
                <Select.Item
                  key={row.id}
                  value={row.id}
                  disabled={row.disabled}
                  nativeButton
                  render={(props, state) => (
                    <InteractiveRow
                      {...(props as ComponentPropsWithRef<"button">)}
                      highlighted={state.highlighted}
                      rowID={row.id}
                      onHighlightedChange={mirrorHighlight}
                    />
                  )}
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
