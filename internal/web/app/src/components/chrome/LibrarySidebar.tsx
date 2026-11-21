import {
  ChevronDown,
  ChevronUp,
  GripVertical,
  Heart,
  History,
  House,
  LayoutGrid,
  ListOrdered,
  type LucideIcon,
  Pencil,
  Pin,
  PinOff,
} from "lucide-react";
import {
  type ComponentProps,
  type CSSProperties,
  type KeyboardEvent,
  useEffect,
  useRef,
  useState,
} from "react";
import { Link } from "react-router";
import { displayString } from "@/lib/display";
import type { LibrarySummary, NavigationNode } from "@/lib/graphql/types";
import { useDisplayLocale, useT } from "@/lib/i18n";
import { libraryIcon } from "@/lib/libraryIcons";
import { pivotOfPath, rememberedLibraryPath } from "@/lib/libraryPivot";
import { nodePath } from "@/lib/nodePath";
import type { StringKey } from "@/strings";
import {
  applySidebarPrefs,
  EMPTY_SIDEBAR_PREFS,
  PERSONAL_ENTRIES,
  type PersonalEntry,
  type SidebarLibraryPrefs,
} from "./sidebarPrefs";

// The "Your stuff" entries' labels and glyphs, the ones the account menu uses.
const PERSONAL_LABELS: Record<PersonalEntry["id"], StringKey> = {
  playlists: "playlistsTitle",
  favorites: "favoritesTitle",
  history: "historyTitle",
};
const PERSONAL_ICONS: Record<PersonalEntry["id"], LucideIcon> = {
  playlists: ListOrdered,
  favorites: Heart,
  history: History,
};

export type SidebarGroup = "libraries" | "personal";

/** The element ID of a sidebar group (heading, tools and list), for moving focus into it. */
export function sidebarGroupId(sidebarId: string, group: SidebarGroup): string {
  return `${sidebarId}-${group}`;
}

// The Viewer's left sidebar (CORE-1131): the manifest's navigation as a
// vertical list, so any number of libraries fits where a header tab strip ran
// out of room. Aggregate entries (Home, Ambient) come first with a glyph;
// library entries follow under a "Libraries" heading, each with a tile tinted
// by the library's own accent and carrying its icon (CORE-1200), or a monogram
// when the server names no icon this client knows. Desktop collapses it to an
// icon rail; phones open it as a drawer. The shell owns that state and passes
// it in. As a drawer the shell renders it as a modal dialog's popup, so the
// remaining <aside> props (the dialog's role, ref and state attributes) land on
// the element itself.
//
// Only views this client renders get an entry: BrowsePage draws "layout" nodes
// and sends anything else to Home, so an Ambient entry would be a link that
// goes nowhere of its own.
//
// Each Profile pins, unpins and reorders its own libraries (CORE-2435): the
// Libraries heading carries an edit button, and the choice (prefs) layers over
// the navigation the server already shaped with its server-wide override.
// Unpinned libraries stay reachable under More.
//
// A signed-in Profile also gets a "Your stuff" group (CORE-2827): its
// Playlists, Favorites and History, one click from any page instead of two in
// the account menu (which keeps them). The group has its own edit button; its
// unpinned entries leave the sidebar.
export function LibrarySidebar({
  id,
  navigation,
  libraries,
  activeNodeId,
  collapsed,
  drawer,
  onNavigate,
  prefs = EMPTY_SIDEBAR_PREFS,
  prefsReady = true,
  onSavePrefs,
  showPersonal = false,
  pathname = "",
  className,
  ...aside
}: Omit<ComponentProps<"aside">, "id" | "children"> & {
  id: string;
  navigation: NavigationNode[];
  libraries: LibrarySummary[];
  activeNodeId: string;
  collapsed: boolean;
  /** Phone layout: the sidebar is an overlay drawer, mounted only while open. */
  drawer: boolean;
  onNavigate: () => void;
  /** The Profile's own pin and order choice. */
  prefs?: SidebarLibraryPrefs;
  /**
   * False while the Profile's saved choice is still loading: an edit begun
   * then would start from server order and overwrite the saved choice.
   */
  prefsReady?: boolean;
  /** Saves an edited choice; without it the sidebar offers no edit mode. */
  onSavePrefs?: (prefs: SidebarLibraryPrefs) => Promise<void>;
  /** Show the signed-in Profile's "Your stuff" group. */
  showPersonal?: boolean;
  /** The current route path: marks the active "Your stuff" entry and keeps the open Library's pivot. */
  pathname?: string;
}) {
  const t = useT();
  const displayLocale = useDisplayLocale();
  const accents = new Map(libraries.map((library) => [library.id, library.accent]));
  const icons = new Map(libraries.map((library) => [library.id, libraryIcon(library.icon)]));
  const rendered = navigation.filter((node) => node.view === "layout");
  const aggregates = rendered.filter((node) => !node.libraryID);
  const libraryNodes = rendered.filter((node) => node.libraryID);
  const libraryIDOf = (node: NavigationNode) => node.libraryID ?? "";
  const { pinned, unpinned } = applySidebarPrefs(libraryNodes, libraryIDOf, prefs);
  const personalIDOf = (entry: PersonalEntry) => entry.id;
  const personal = applySidebarPrefs(
    [...PERSONAL_ENTRIES],
    personalIDOf,
    prefs.personal ?? EMPTY_SIDEBAR_PREFS,
  );
  const [moreOpen, setMoreOpen] = useState(false);
  // One group is edited at a time.
  const [editing, setEditing] = useState<{ group: SidebarGroup; draft: EditDraft } | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveFailed, setSaveFailed] = useState<SidebarGroup | null>(null);
  // A second Done in the same tick sees stale state; the ref does not.
  const savingRef = useRef(false);
  const editButtonRefs = {
    libraries: useRef<HTMLButtonElement>(null),
    personal: useRef<HTMLButtonElement>(null),
  };
  // Leaving edit mode hands focus back to the Edit button it came from.
  const restoreFocus = useRef<SidebarGroup | null>(null);
  useEffect(() => {
    const group = restoreFocus.current;
    if (editing || !group) return;
    restoreFocus.current = null;
    (group === "libraries" ? editButtonRefs.libraries : editButtonRefs.personal).current?.focus();
  });
  function leaveEditing(group: SidebarGroup) {
    restoreFocus.current = group;
    setEditing(null);
  }
  const canEditAny = !!onSavePrefs && (!collapsed || drawer);
  const canEdit = canEditAny && libraryNodes.length > 0;
  // Collapsing to the icon rail (or losing the saver) shows plain navigation;
  // the draft is kept, so expanding again picks the edit up where it was.
  const activeDraft = canEdit && editing?.group === "libraries" ? editing.draft : null;
  const personalDraft =
    canEditAny && showPersonal && editing?.group === "personal" ? editing.draft : null;
  const moreId = `${id}-more`;
  const homeId = rendered[0]?.id;
  // Only tiles without an icon carry a monogram, so only those can collide.
  const lettered = libraryNodes.filter((node) => !icons.get(node.libraryID ?? ""));
  const monograms = libraryMonograms(
    lettered.map((node) => displayString(node.title, displayLocale)),
  );
  const monogramFor = new Map(lettered.map((node, index) => [node.id, monograms[index]]));

  function item(node: NavigationNode) {
    const title = displayString(node.title, displayLocale);
    const active = node.id === activeNodeId;
    const accent = node.libraryID ? accents.get(node.libraryID) : undefined;
    const Icon = node.libraryID ? icons.get(node.libraryID) : undefined;
    return (
      <li key={node.id}>
        <Link
          // A Library reopens on the pivot last used there (CORE-2826); the
          // open one keeps the pivot on screen, which is stored only after render.
          to={
            !node.libraryID
              ? nodePath(node.id)
              : active
                ? nodePath(node.id, pivotOfPath(pathname))
                : rememberedLibraryPath(node.id)
          }
          aria-current={active ? "page" : undefined}
          className={"bf-side-link" + (active ? " is-active" : "")}
          title={collapsed && !drawer ? title : undefined}
          onClick={onNavigate}
        >
          {node.libraryID ? (
            <span
              aria-hidden
              className="bf-side-tile"
              style={accent ? ({ "--tile": accent } as CSSProperties) : undefined}
            >
              {Icon ? <Icon className="size-4" strokeWidth={2.25} /> : monogramFor.get(node.id)}
            </span>
          ) : (
            <span aria-hidden className="bf-side-glyph">
              {node.id === homeId ? (
                <House className="size-[18px]" />
              ) : (
                <LayoutGrid className="size-[18px]" />
              )}
            </span>
          )}
          <span className="bf-side-label">{title}</span>
        </Link>
      </li>
    );
  }

  function personalItem(entry: PersonalEntry) {
    const title = t(PERSONAL_LABELS[entry.id]);
    const Icon = PERSONAL_ICONS[entry.id];
    const active = pathname === entry.path || pathname.startsWith(`${entry.path}/`);
    return (
      <li key={entry.id}>
        <Link
          to={entry.path}
          aria-current={active ? "page" : undefined}
          className={"bf-side-link" + (active ? " is-active" : "")}
          title={collapsed && !drawer ? title : undefined}
          onClick={onNavigate}
        >
          <span aria-hidden className="bf-side-glyph">
            <Icon className="size-[18px]" />
          </span>
          <span className="bf-side-label">{title}</span>
        </Link>
      </li>
    );
  }

  function editButton(group: SidebarGroup, label: string, start: () => EditDraft) {
    return (
      <button
        ref={editButtonRefs[group]}
        type="button"
        className="bf-side-tool"
        aria-label={label}
        aria-disabled={!prefsReady}
        title={label}
        onClick={() => {
          if (!prefsReady) return;
          setSaveFailed(null);
          setEditing({ group, draft: start() });
        }}
      >
        <Pencil aria-hidden className="size-3.5" />
      </button>
    );
  }

  function editor(group: SidebarGroup, draft: EditDraft, titleOf: (key: string) => string) {
    return (
      <SidebarEditor
        draft={draft}
        titleOf={titleOf}
        saving={saving}
        onChange={(next) => setEditing({ group, draft: next })}
        onCancel={() => {
          if (!savingRef.current) leaveEditing(group);
        }}
        onDone={async () => {
          if (!onSavePrefs || savingRef.current) return;
          savingRef.current = true;
          setSaving(true);
          setSaveFailed(null);
          const choice = { order: draft.order, hidden: [...draft.hidden] };
          // An order the Profile never chose is not stored, so a later change
          // to the defaults still reaches it (CORE-2880). Pin toggles leave
          // the draft order alone; only a move, or a stored order, is a choice.
          const unmoved =
            (prefs.personal?.order.length ?? 0) === 0 &&
            sameOrder(
              draft.order,
              startDraft(personal.pinned, personal.unpinned, personalIDOf).order,
            );
          const personalChoice =
            unmoved || sameOrder(draft.order, PERSONAL_ENTRIES.map(personalIDOf))
              ? { ...choice, order: [] }
              : choice;
          try {
            await onSavePrefs(
              group === "libraries"
                ? { ...prefs, ...choice }
                : { ...prefs, personal: personalChoice },
            );
            leaveEditing(group);
          } catch {
            setSaveFailed(group);
          } finally {
            savingRef.current = false;
            setSaving(false);
          }
        }}
      />
    );
  }

  function saveError(group: SidebarGroup) {
    return (
      saveFailed === group && (
        <p role="alert" className="bf-side-error">
          {t("sidebarSaveFailed")}
        </p>
      )
    );
  }

  return (
    <aside
      {...aside}
      id={id}
      className={
        "bf-sidebar" +
        (collapsed && !drawer ? " is-collapsed" : "") +
        (drawer ? " is-drawer" : "") +
        (className ? ` ${className}` : "")
      }
    >
      <nav aria-label={t("navLabel")}>
        <ul className="bf-side-list">{aggregates.map(item)}</ul>
        {libraryNodes.length > 0 && (
          <div id={sidebarGroupId(id, "libraries")}>
            <div className="bf-side-heading-row">
              <h2 className="bf-side-heading">{t("libraries")}</h2>
              {canEdit &&
                !editing &&
                editButton("libraries", t("sidebarEdit"), () =>
                  startDraft(pinned, unpinned, libraryIDOf),
                )}
            </div>
            {activeDraft ? (
              editor("libraries", activeDraft, (libraryID) => {
                const node = libraryNodes.find((n) => n.libraryID === libraryID);
                return node ? displayString(node.title, displayLocale) : libraryID;
              })
            ) : (
              <>
                <ul className="bf-side-list">{pinned.map(item)}</ul>
                {unpinned.length > 0 && (
                  <>
                    <button
                      type="button"
                      className="bf-side-more"
                      aria-expanded={moreOpen}
                      aria-controls={moreOpen ? moreId : undefined}
                      onClick={() => setMoreOpen((open) => !open)}
                    >
                      <span className="bf-side-label">{t("sidebarMore")}</span>
                      {moreOpen ? (
                        <ChevronUp aria-hidden className="size-4" />
                      ) : (
                        <ChevronDown aria-hidden className="size-4" />
                      )}
                    </button>
                    {moreOpen && (
                      <ul id={moreId} className="bf-side-list">
                        {unpinned.map(item)}
                      </ul>
                    )}
                  </>
                )}
              </>
            )}
            {saveError("libraries")}
          </div>
        )}
        {showPersonal && (
          <div id={sidebarGroupId(id, "personal")}>
            <div className="bf-side-heading-row">
              <h2 className="bf-side-heading">{t("yourStuff")}</h2>
              {canEditAny &&
                !editing &&
                editButton("personal", t("sidebarEditPersonal"), () =>
                  startDraft(personal.pinned, personal.unpinned, personalIDOf),
                )}
            </div>
            {personalDraft
              ? editor("personal", personalDraft, (entryID) => {
                  const key = PERSONAL_LABELS[entryID as PersonalEntry["id"]];
                  return key ? t(key) : entryID;
                })
              : personal.pinned.length > 0 && (
                  <ul className="bf-side-list">{personal.pinned.map(personalItem)}</ul>
                )}
            {saveError("personal")}
          </div>
        )}
      </nav>
    </aside>
  );
}

// An edit session's working copy: every entry of the group (each library the
// server lists, or each "Your stuff" entry), in the order being edited, and
// the ones unpinned.
interface EditDraft {
  order: string[];
  hidden: Set<string>;
}

function startDraft<T>(pinned: T[], unpinned: T[], libraryIDOf: (entry: T) => string): EditDraft {
  return {
    order: [...pinned, ...unpinned].map(libraryIDOf),
    hidden: new Set(unpinned.map(libraryIDOf)),
  };
}

/** Move one entry of a list from one index to another. */
export function moveEntry<T>(list: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return list;
  const next = [...list];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
}

// The edit mode: one row per library with a pin toggle and move buttons.
// Rows drag to reorder; for the keyboard, the move buttons and Alt+Up/Down on
// a focused row do the same, and a live region announces the new position.
function SidebarEditor({
  draft,
  titleOf,
  saving,
  onChange,
  onCancel,
  onDone,
}: {
  draft: EditDraft;
  titleOf: (libraryID: string) => string;
  saving: boolean;
  onChange: (draft: EditDraft) => void;
  onCancel: () => void;
  onDone: () => void;
}) {
  const t = useT();
  const [dragging, setDragging] = useState<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const count = draft.order.length;
  const listRef = useRef<HTMLUListElement>(null);
  // Entering edit mode moves focus to the first control, where the Edit
  // button that started it no longer is.
  useEffect(() => {
    listRef.current?.querySelector("button")?.focus();
  }, []);

  function move(from: number, to: number) {
    if (saving) return;
    const order = moveEntry(draft.order, from, to);
    if (order === draft.order) return;
    onChange({ ...draft, order });
    setAnnouncement(
      t("sidebarMoved", { name: titleOf(draft.order[from]), position: to + 1, count }),
    );
  }

  function togglePin(libraryID: string) {
    if (saving) return;
    const hidden = new Set(draft.hidden);
    if (hidden.has(libraryID)) hidden.delete(libraryID);
    else hidden.add(libraryID);
    onChange({ ...draft, hidden });
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
    <div className="bf-side-editor">
      <ul ref={listRef} className="bf-side-list">
        {draft.order.map((libraryID, index) => {
          const title = titleOf(libraryID);
          const isPinned = !draft.hidden.has(libraryID);
          return (
            <li
              key={libraryID}
              className={
                "bf-side-edit-row" +
                (isPinned ? "" : " is-unpinned") +
                (dragging === libraryID ? " is-dragging" : "")
              }
              draggable={!saving}
              onDragStart={(event) => {
                if (saving) return;
                setDragging(libraryID);
                event.dataTransfer?.setData("text/plain", libraryID);
                if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
              }}
              onDragOver={(event) => {
                if (dragging) event.preventDefault();
              }}
              onDrop={(event) => {
                event.preventDefault();
                if (dragging) move(draft.order.indexOf(dragging), index);
                setDragging(null);
              }}
              onDragEnd={() => setDragging(null)}
              onKeyDown={(event) => onRowKeyDown(event, index)}
            >
              <span aria-hidden className="bf-side-grip" title={t("sidebarDragHint")}>
                <GripVertical className="size-4" />
              </span>
              <span className="bf-side-label">{title}</span>
              <button
                type="button"
                className="bf-side-tool"
                aria-pressed={isPinned}
                aria-label={t("sidebarPin", { name: title })}
                aria-disabled={saving || undefined}
                onClick={() => togglePin(libraryID)}
              >
                {isPinned ? (
                  <Pin aria-hidden className="size-4" />
                ) : (
                  <PinOff aria-hidden className="size-4" />
                )}
              </button>
              <button
                type="button"
                className="bf-side-tool"
                aria-label={t("sidebarMoveUp", { name: title })}
                aria-disabled={saving || index === 0}
                onClick={() => move(index, index - 1)}
              >
                <ChevronUp aria-hidden className="size-4" />
              </button>
              <button
                type="button"
                className="bf-side-tool"
                aria-label={t("sidebarMoveDown", { name: title })}
                aria-disabled={saving || index === count - 1}
                onClick={() => move(index, index + 1)}
              >
                <ChevronDown aria-hidden className="size-4" />
              </button>
            </li>
          );
        })}
      </ul>
      <div className="bf-side-edit-actions">
        <button
          type="button"
          className="bf-side-action"
          onClick={onCancel}
          aria-disabled={saving || undefined}
        >
          {t("sidebarCancel")}
        </button>
        <button
          type="button"
          className="bf-side-action is-primary"
          onClick={onDone}
          aria-disabled={saving || undefined}
        >
          {t("sidebarDone")}
        </button>
      </div>
      <p aria-live="polite" className="sr-only">
        {announcement}
      </p>
    </div>
  );
}

// Library tile monograms: what a tile carries when the manifest names no icon
// this client knows (an older server, or a name newer than this build). The
// tile is then drawn from the title: its initial, or two characters wherever
// initials would collide, so "Podcasts" and "Photos" read "Po" and "Ph" rather
// than a pair of identical tiles.
export function libraryMonograms(titles: string[]): string[] {
  const initials = titles.map((title) => monogram(title, false));
  return titles.map((title, index) =>
    initials.indexOf(initials[index]) !== initials.lastIndexOf(initials[index])
      ? monogram(title, true)
      : initials[index],
  );
}

function monogram(title: string, long: boolean): string {
  const words = title.trim().split(/\s+/).filter(Boolean);
  const [first = "•", second = ""] = Array.from(words[0] ?? "");
  if (!long) return first.toUpperCase();
  // Several words: their initials ("Home Videos" is "HV"). One word: its
  // first two characters.
  const next = words.length > 1 ? (Array.from(words[1])[0]?.toUpperCase() ?? "") : second;
  return first.toUpperCase() + next;
}

function sameOrder(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((id, index) => id === b[index]);
}
