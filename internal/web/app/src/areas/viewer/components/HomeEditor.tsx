import { ChevronDown, ChevronUp, GripVertical } from "lucide-react";
import { type KeyboardEvent, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { displayString } from "@/lib/display";
import type { HomeLayoutSection } from "@/lib/graphql/types";
import { fetchHomeLayout, resetHomeLayout, setHomeLayout } from "@/lib/graphql/viewer";
import { useDisplayLocale, useT } from "@/lib/i18n";

interface HomeEditorProps {
  /**
   * Called after a save or reset lands, so the caller can re-read the Home it
   * shows. Use the in-epoch manifest refresh, not a session reload, which
   * would unmount the player.
   */
  onSaved: () => void;
  onClose: () => void;
}

// "Edit home" mode (CORE-1972): the Profile reorders and hides its Home shelves.
// Every shelf comes from the server's homeLayout, which lists only what this
// Profile may see, so editing never reveals anything new.
export function HomeEditor({ onSaved, onClose }: HomeEditorProps) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const [sections, setSections] = useState<HomeLayoutSection[] | null>(null);
  const [dragging, setDragging] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    fetchHomeLayout(controller.signal).then(
      (layout) => setSections(layout.sections),
      () => {
        if (!controller.signal.aborted) setFailed(true);
      },
    );
    return () => controller.abort();
  }, []);

  function move(from: number, to: number) {
    if (!sections || saving || to < 0 || to >= sections.length || from === to) return;
    const next = [...sections];
    const [section] = next.splice(from, 1);
    next.splice(to, 0, section);
    setSections(next);
  }

  function toggle(id: string, visible: boolean) {
    if (!sections || saving) return;
    setSections(sections.map((s) => (s.id === id ? { ...s, hidden: !visible } : s)));
  }

  async function commit(write: () => Promise<unknown>) {
    setSaving(true);
    setFailed(false);
    try {
      await write();
      onSaved();
      onClose();
    } catch {
      setFailed(true);
    } finally {
      setSaving(false);
    }
  }

  function save() {
    if (!sections) return;
    void commit(() =>
      setHomeLayout(
        sections.map((s) => s.id),
        sections.filter((s) => s.hidden).map((s) => s.id),
      ),
    );
  }

  function onRowKeyDown(event: KeyboardEvent, index: number) {
    if (!event.altKey) return;
    if (event.key === "ArrowUp") {
      event.preventDefault();
      move(index, index - 1);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      move(index, index + 1);
    }
  }

  return (
    <section aria-labelledby="home-editor-title" className="flex max-w-2xl flex-col gap-4">
      <h2 id="home-editor-title" className="text-xl font-semibold">
        {t("homeEdit")}
      </h2>
      <p className="text-sm text-[var(--text-secondary)]">{t("homeEditHelp")}</p>
      {failed && (
        <p role="alert" className="text-sm text-destructive">
          {t("homeEditFailed")}
        </p>
      )}
      {sections === null ? (
        !failed && <p aria-busy="true">{t("loading")}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {sections.map((section, index) => {
            const name = displayString(section.title, displayLocale) || section.id;
            return (
              <li
                key={section.id}
                data-section-id={section.id}
                className={
                  "flex items-center gap-3 rounded-md border border-[var(--border)] px-3 py-2" +
                  (section.hidden ? " opacity-60" : "") +
                  (dragging === section.id ? " ring-2 ring-primary" : "")
                }
                draggable={!saving}
                onDragStart={(event) => {
                  setDragging(section.id);
                  event.dataTransfer?.setData("text/plain", section.id);
                  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                }}
                onDragOver={(event) => {
                  if (dragging) event.preventDefault();
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  if (dragging)
                    move(
                      sections.findIndex((s) => s.id === dragging),
                      index,
                    );
                  setDragging(null);
                }}
                onDragEnd={() => setDragging(null)}
                onKeyDown={(event) => onRowKeyDown(event, index)}
              >
                <GripVertical aria-hidden className="size-4 cursor-grab" />
                <span className="flex-1">{name}</span>
                <Switch
                  checked={!section.hidden}
                  disabled={saving}
                  aria-label={t("homeEditVisible", { name })}
                  onCheckedChange={(visible) => toggle(section.id, visible)}
                />
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("homeEditMoveUp", { name })}
                  disabled={saving || index === 0}
                  onClick={() => move(index, index - 1)}
                >
                  <ChevronUp aria-hidden />
                </Button>
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("homeEditMoveDown", { name })}
                  disabled={saving || index === sections.length - 1}
                  onClick={() => move(index, index + 1)}
                >
                  <ChevronDown aria-hidden />
                </Button>
              </li>
            );
          })}
        </ul>
      )}
      <div className="flex flex-wrap gap-2">
        <Button disabled={saving || sections === null} onClick={save}>
          {t("homeEditSave")}
        </Button>
        <Button variant="outline" disabled={saving} onClick={onClose}>
          {t("homeEditCancel")}
        </Button>
        <Button
          variant="ghost"
          className="ml-auto"
          disabled={saving}
          onClick={() => void commit(resetHomeLayout)}
        >
          {t("homeEditReset")}
        </Button>
      </div>
    </section>
  );
}
