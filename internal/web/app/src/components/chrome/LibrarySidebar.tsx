import {
  ChevronDown,
  ChevronRight,
  ChevronUp,
  FolderInput,
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
  Plus,
  Trash2,
  X,
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
  resolveSidebarLibraries,
  type SidebarLayoutItem,
  type SidebarLibraryGroup,
  type SidebarLibraryPrefs,
  type SidebarResolvedNode,
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
  const { topLevel, unpinned } = resolveSidebarLibraries(libraryNodes, libraryIDOf, prefs);
  const personalIDOf = (entry: PersonalEntry) => entry.id;
  const personal = applySidebarPrefs(
    [...PERSONAL_ENTRIES],
    personalIDOf,
    prefs.personal ?? EMPTY_SIDEBAR_PREFS,
  );
  const [moreOpen, setMoreOpen] = useState(false);
  // Local collapse overrides handle standalone/mocked renders where prefs doesn't
  // update immediately; synced back whenever the prefs object changes.
  const [toggleState, setToggleState] = useState<{
    prefs: SidebarLibraryPrefs;
    overrides: Record<string, boolean>;
  }>({ prefs, overrides: {} });
  const activeLibraryID = libraryNodes.find((n) => n.id === activeNodeId)?.libraryID ?? "";
  // Groups manually collapsed while their active library was open; resets when
  // navigating to a different library so the new active library's group auto-expands.
  const [collapsedWhileActive, setCollapsedWhileActive] = useState<{
    libraryID: string;
    groupIDs: Set<string>;
  }>({ libraryID: activeLibraryID, groupIDs: new Set() });
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
  const collapseOverrides = toggleState.prefs === prefs ? toggleState.overrides : {};
  const activeCollapsedSet =
    collapsedWhileActive.libraryID === activeLibraryID ? collapsedWhileActive.groupIDs : null;

  function isGroupExpanded(group: Extract<SidebarResolvedNode<NavigationNode>, { kind: "group" }>) {
    const containsActive =
      activeLibraryID !== "" && group.items.some((entry) => entry.libraryID === activeLibraryID);
    if (containsActive) {
      return !(activeCollapsedSet?.has(group.id) ?? false);
    }
    const prefCollapsed = collapseOverrides[group.id] ?? group.collapsed;
    return !prefCollapsed;
  }

  function toggleGroupCollapsed(groupID: string, currentlyExpanded: boolean) {
    const nextCollapsed = currentlyExpanded;
    setToggleState({
      prefs,
      overrides: { ...collapseOverrides, [groupID]: nextCollapsed },
    });
    if (activeLibraryID !== "") {
      const nextSet = new Set(activeCollapsedSet ?? []);
      if (nextCollapsed) nextSet.add(groupID);
      else nextSet.delete(groupID);
      setCollapsedWhileActive({ libraryID: activeLibraryID, groupIDs: nextSet });
    }
    if (onSavePrefs && prefsReady && prefs.groups?.some((group) => group.id === groupID)) {
      const nextGroups: SidebarLibraryGroup[] = prefs.groups.map((group) => {
        if (group.id !== groupID) return group;
        return nextCollapsed
          ? { id: group.id, name: group.name, libraries: group.libraries, collapsed: true }
          : { id: group.id, name: group.name, libraries: group.libraries };
      });
      void onSavePrefs({ ...prefs, groups: nextGroups }).catch(() => {});
    }
  }

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
                ? nodePath(node.id, pivotOfPath(pathname), node.libraryID)
                : rememberedLibraryPath(node.id, node.libraryID)
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

  function editor(
    group: SidebarGroup,
    draft: EditDraft,
    titleOf: (key: string) => string,
    allowGrouping: boolean,
  ) {
    return (
      <SidebarEditor
        draft={draft}
        titleOf={titleOf}
        allowGrouping={allowGrouping}
        saving={saving}
        onChange={(next) => setEditing({ group, draft: next })}
        onCancel={() => {
          if (!savingRef.current) leaveEditing(group);
        }}
        onDone={async (finalDraft) => {
          if (!onSavePrefs || savingRef.current) return;
          savingRef.current = true;
          setSaving(true);
          setSaveFailed(null);
          try {
            if (group === "libraries") {
              await onSavePrefs(buildLibraryPrefsFromDraft(finalDraft, prefs));
            } else {
              const order = flattenLibraryOrder(finalDraft.nodes);
              const choice = { order, hidden: [...finalDraft.hidden] };
              // Only a move changes the stored order: pin toggles keep whatever
              // order was stored, or none, so a later change to the defaults
              // still reaches an order the Profile never chose (CORE-2880).
              const moved = !sameOrder(order, finalDraft.startLibraryOrder);
              const personalChoice = moved
                ? choice
                : { ...choice, order: prefs.personal?.order ?? [] };
              await onSavePrefs({ ...prefs, personal: personalChoice });
            }
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

  function renderTopLevelNode(node: SidebarResolvedNode<NavigationNode>) {
    if (node.kind === "divider") {
      return (
        <li key={`divider:${node.id}`} className="bf-side-divider-item">
          <hr className="bf-side-divider" />
        </li>
      );
    }
    if (node.kind === "library") {
      return item(node.entry);
    }
    const expanded = isGroupExpanded(node);
    const hasActiveChild = node.items.some((child) => child.entry.id === activeNodeId);
    const sublistId = `${id}-group-${node.id}`;
    return (
      <li key={`group:${node.id}`} className="bf-side-group">
        <button
          type="button"
          className={"bf-side-group-toggle" + (!expanded && hasActiveChild ? " has-active" : "")}
          aria-expanded={expanded}
          aria-controls={expanded ? sublistId : undefined}
          onClick={() => toggleGroupCollapsed(node.id, expanded)}
        >
          <ChevronRight aria-hidden className="bf-side-group-chevron size-4" />
          <span className="bf-side-label">{node.name}</span>
        </button>
        {expanded && (
          <ul id={sublistId} className="bf-side-sublist">
            {node.items.map((child) => item(child.entry))}
          </ul>
        )}
      </li>
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
                  startLibraryDraft(libraryNodes, libraryIDOf, prefs),
                )}
            </div>
            {activeDraft ? (
              editor(
                "libraries",
                activeDraft,
                (libraryID) => {
                  const node = libraryNodes.find((n) => n.libraryID === libraryID);
                  return node ? displayString(node.title, displayLocale) : libraryID;
                },
                true,
              )
            ) : (
              <>
                <ul className="bf-side-list">
                  {collapsed && !drawer
                    ? topLevel.flatMap((node) =>
                        node.kind === "group"
                          ? node.items.map((child) => item(child.entry))
                          : node.kind === "library"
                            ? [item(node.entry)]
                            : [
                                <li key={`divider:${node.id}`} className="bf-side-divider-item">
                                  <hr className="bf-side-divider" />
                                </li>,
                              ],
                      )
                    : topLevel.map(renderTopLevelNode)}
                </ul>
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
                  startSimpleDraft(personal.pinned, personal.unpinned, personalIDOf),
                )}
            </div>
            {personalDraft
              ? editor(
                  "personal",
                  personalDraft,
                  (entryID) => {
                    const key = PERSONAL_LABELS[entryID as PersonalEntry["id"]];
                    return key ? t(key) : entryID;
                  },
                  false,
                )
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

type EditNode =
  | { kind: "library"; id: string }
  | { kind: "group"; id: string; name: string; libraries: string[]; collapsed?: boolean }
  | { kind: "divider"; id: string };

// An edit session's working copy: top-level libraries, groups (holding their
// assigned libraries), and dividers, plus the set of unpinned library IDs.
interface EditDraft {
  nodes: EditNode[];
  hidden: Set<string>;
  /** The flattened library order the editor opened with; detects no-op moves. */
  startLibraryOrder: string[];
}

function flattenLibraryOrder(nodes: EditNode[]): string[] {
  const order: string[] = [];
  for (const node of nodes) {
    if (node.kind === "library") order.push(node.id);
    else if (node.kind === "group") order.push(...node.libraries);
  }
  return order;
}

function startSimpleDraft<T>(
  pinned: T[],
  unpinned: T[],
  libraryIDOf: (entry: T) => string,
): EditDraft {
  const order = [...pinned, ...unpinned].map(libraryIDOf);
  return {
    nodes: order.map((id) => ({ kind: "library", id })),
    hidden: new Set(unpinned.map(libraryIDOf)),
    startLibraryOrder: order,
  };
}

function startLibraryDraft(
  libraryNodes: NavigationNode[],
  libraryIDOf: (node: NavigationNode) => string,
  prefs: SidebarLibraryPrefs,
): EditDraft {
  const groups = prefs.groups ?? [];
  const layout = prefs.layout ?? [];
  const hasStructure = groups.length > 0 || layout.some((item) => item.type === "divider");
  if (!hasStructure) {
    const { pinned, unpinned } = applySidebarPrefs(libraryNodes, libraryIDOf, prefs);
    return startSimpleDraft(pinned, unpinned, libraryIDOf);
  }

  const serverIDs = libraryNodes.map(libraryIDOf);
  const serverSet = new Set(serverIDs);
  const hidden = new Set(prefs.hidden);
  const placedLibs = new Set<string>();
  const groupByID = new Map<string, Extract<EditNode, { kind: "group" }>>();

  for (const group of groups) {
    const libs: string[] = [];
    for (const libID of group.libraries) {
      if (!serverSet.has(libID) || placedLibs.has(libID)) continue;
      placedLibs.add(libID);
      libs.push(libID);
    }
    groupByID.set(group.id, {
      kind: "group",
      id: group.id,
      name: group.name,
      libraries: libs,
      ...(group.collapsed ? { collapsed: true } : {}),
    });
  }

  const nodes: EditNode[] = [];
  const seenGroups = new Set<string>();
  const seenDividers = new Set<string>();

  for (const item of layout) {
    if (item.type === "divider") {
      if (seenDividers.has(item.id)) continue;
      seenDividers.add(item.id);
      nodes.push({ kind: "divider", id: item.id });
    } else if (item.type === "group") {
      const group = groupByID.get(item.id);
      if (!group || seenGroups.has(group.id)) continue;
      seenGroups.add(group.id);
      nodes.push(group);
    } else if (item.type === "library") {
      if (!serverSet.has(item.id) || placedLibs.has(item.id)) continue;
      placedLibs.add(item.id);
      nodes.push({ kind: "library", id: item.id });
    }
  }

  for (const group of groupByID.values()) {
    if (!seenGroups.has(group.id)) nodes.push(group);
  }

  // Keep any remaining libraries (e.g. newly added on the server) at top level,
  // pinned first in server order, followed by unpinned in server order.
  const remaining = serverIDs.filter((id) => !placedLibs.has(id));
  for (const id of remaining) {
    if (!hidden.has(id)) nodes.push({ kind: "library", id });
  }
  for (const id of remaining) {
    if (hidden.has(id)) nodes.push({ kind: "library", id });
  }

  return {
    nodes,
    hidden,
    startLibraryOrder: flattenLibraryOrder(nodes),
  };
}

function buildLibraryPrefsFromDraft(
  draft: EditDraft,
  existingPrefs: SidebarLibraryPrefs,
): SidebarLibraryPrefs {
  const order = flattenLibraryOrder(draft.nodes);
  const hidden = [...draft.hidden];
  const groups: SidebarLibraryGroup[] = [];
  const layout: SidebarLayoutItem[] = [];
  let hasDividers = false;

  for (const node of draft.nodes) {
    if (node.kind === "library") {
      layout.push({ type: "library", id: node.id });
    } else if (node.kind === "divider") {
      hasDividers = true;
      layout.push({ type: "divider", id: node.id });
    } else {
      groups.push({
        id: node.id,
        name: node.name,
        libraries: node.libraries,
        ...(node.collapsed ? { collapsed: true } : {}),
      });
      layout.push({ type: "group", id: node.id });
    }
  }

  const next: SidebarLibraryPrefs = { order, hidden };
  if (existingPrefs.personal) next.personal = existingPrefs.personal;
  if (groups.length > 0 || hasDividers) {
    if (groups.length > 0) next.groups = groups;
    next.layout = layout;
  }
  return next;
}

/** Move one entry of a list from one index to another. */
export function moveEntry<T>(list: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return list;
  const next = [...list];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
}

function nextUniqueId(prefix: string, usedIDs: Iterable<string>): string {
  let max = 0;
  const pattern = new RegExp(`^${prefix}-(\\d+)$`);
  for (const id of usedIDs) {
    const match = pattern.exec(id);
    if (match) max = Math.max(max, Number(match[1]));
  }
  return `${prefix}-${max + 1}`;
}

type DragSource =
  | { kind: "top"; index: number; nodeKind: EditNode["kind"]; id: string }
  | { kind: "grouped"; groupID: string; libID: string; index: number };

type DropTarget =
  | { kind: "top"; index: number }
  | { kind: "group"; groupID: string }
  | { kind: "grouped"; groupID: string; index: number };

// The edit mode: top-level rows (Libraries, collapsible groups, and dividers),
// plus Libraries nested inside groups. Rows drag to reorder; buttons and
// Alt+Up/Down do the same for keyboard users, and a live region announces moves.
function SidebarEditor({
  draft,
  titleOf,
  allowGrouping,
  saving,
  onChange,
  onCancel,
  onDone,
}: {
  draft: EditDraft;
  titleOf: (libraryID: string) => string;
  allowGrouping: boolean;
  saving: boolean;
  onChange: (draft: EditDraft) => void;
  onCancel: () => void;
  onDone: (finalDraft: EditDraft) => void;
}) {
  const t = useT();
  const [dragging, setDragging] = useState<DragSource | null>(null);
  const [dropTarget, setDropTarget] = useState<DropTarget | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; value: string } | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const topCount = draft.nodes.length;
  const listRef = useRef<HTMLUListElement>(null);
  const addRef = useRef<HTMLDivElement>(null);
  const renameInputRef = useRef<HTMLInputElement>(null);
  const pendingFocusGroup = useRef<string | null>(null);
  const pendingFocusAfterDelete = useRef(false);

  // Entering edit mode moves focus to the first control, where the Edit
  // button that started it no longer is.
  useEffect(() => {
    listRef.current?.querySelector("button")?.focus();
  }, []);

  useEffect(() => {
    if (!renaming) return;
    renameInputRef.current?.focus();
    renameInputRef.current?.select();
  }, [renaming?.id]);

  useEffect(() => {
    if (pendingFocusGroup.current) {
      const groupID = pendingFocusGroup.current;
      pendingFocusGroup.current = null;
      const btn = listRef.current?.querySelector<HTMLButtonElement>(
        `[data-group-rename="${groupID}"]`,
      );
      btn?.focus();
    } else if (pendingFocusAfterDelete.current) {
      pendingFocusAfterDelete.current = false;
      const fallback =
        listRef.current?.querySelector<HTMLButtonElement>("button") ??
        addRef.current?.querySelector<HTMLButtonElement>("button");
      fallback?.focus();
    }
  });

  const groups = draft.nodes.filter(
    (node): node is Extract<EditNode, { kind: "group" }> => node.kind === "group",
  );
  const dividerNodes = draft.nodes.filter((node) => node.kind === "divider");
  const hasGroups = groups.length > 0;

  function dividerLabel(dividerID: string): string {
    if (dividerNodes.length <= 1) return t("sidebarDivider");
    const index = dividerNodes.findIndex((node) => node.id === dividerID) + 1;
    return t("sidebarDividerNumbered", { index });
  }

  function nodeLabel(node: EditNode): string {
    if (node.kind === "library") return titleOf(node.id);
    if (node.kind === "group") return node.name;
    return dividerLabel(node.id);
  }

  function applyRename(currentDraft: EditDraft, groupID: string, rawValue: string): EditDraft {
    const trimmed = rawValue.trim();
    if (!trimmed) return currentDraft;
    return {
      ...currentDraft,
      nodes: currentDraft.nodes.map((node) =>
        node.kind === "group" && node.id === groupID ? { ...node, name: trimmed } : node,
      ),
    };
  }

  function commitRename(groupID: string, restoreButtonFocus = false) {
    if (!renaming || renaming.id !== groupID) return;
    const nextDraft = applyRename(draft, groupID, renaming.value);
    if (nextDraft !== draft) onChange(nextDraft);
    setRenaming(null);
    if (restoreButtonFocus) pendingFocusGroup.current = groupID;
  }

  function cancelRename(groupID: string) {
    if (!renaming || renaming.id !== groupID) return;
    setRenaming(null);
    pendingFocusGroup.current = groupID;
  }

  function moveTop(from: number, to: number) {
    if (saving) return;
    const nodes = moveEntry(draft.nodes, from, to);
    if (nodes === draft.nodes) return;
    onChange({ ...draft, nodes });
    setAnnouncement(
      t("sidebarMoved", { name: nodeLabel(draft.nodes[from]), position: to + 1, count: topCount }),
    );
  }

  function moveInGroup(groupID: string, from: number, to: number) {
    if (saving) return;
    const group = draft.nodes.find(
      (node): node is Extract<EditNode, { kind: "group" }> =>
        node.kind === "group" && node.id === groupID,
    );
    if (!group) return;
    const libraries = moveEntry(group.libraries, from, to);
    if (libraries === group.libraries) return;
    const nodes = draft.nodes.map((node) =>
      node.kind === "group" && node.id === groupID ? { ...node, libraries } : node,
    );
    onChange({ ...draft, nodes });
    setAnnouncement(
      t("sidebarMoved", {
        name: titleOf(group.libraries[from]),
        position: to + 1,
        count: libraries.length,
      }),
    );
  }

  function togglePin(libraryID: string) {
    if (saving) return;
    const hidden = new Set(draft.hidden);
    if (hidden.has(libraryID)) hidden.delete(libraryID);
    else hidden.add(libraryID);
    onChange({ ...draft, hidden });
  }

  function moveLibraryToGroup(
    libraryID: string,
    targetGroupID: string | null,
    targetIndex?: number,
  ) {
    if (saving) return;
    const targetGroup = targetGroupID
      ? groups.find((group) => group.id === targetGroupID)
      : undefined;
    if (targetGroupID && !targetGroup) return;

    // Remove the library from its current spot (top-level or inside a group).
    let nodes: EditNode[] = draft.nodes
      .filter((node) => !(node.kind === "library" && node.id === libraryID))
      .map((node) =>
        node.kind === "group"
          ? { ...node, libraries: node.libraries.filter((id) => id !== libraryID) }
          : node,
      );

    if (targetGroupID) {
      nodes = nodes.map((node) => {
        if (node.kind !== "group" || node.id !== targetGroupID) return node;
        const libs = [...node.libraries];
        const insertAt =
          targetIndex !== undefined ? Math.max(0, Math.min(targetIndex, libs.length)) : libs.length;
        libs.splice(insertAt, 0, libraryID);
        return { ...node, libraries: libs };
      });
      onChange({ ...draft, nodes });
      if (targetGroup) {
        setAnnouncement(
          t("sidebarMovedToGroup", { name: titleOf(libraryID), group: targetGroup.name }),
        );
      }
    } else {
      const insertAt =
        targetIndex !== undefined ? Math.max(0, Math.min(targetIndex, nodes.length)) : nodes.length;
      nodes.splice(insertAt, 0, { kind: "library", id: libraryID });
      onChange({ ...draft, nodes });
      setAnnouncement(t("sidebarRemovedFromGroup", { name: titleOf(libraryID) }));
    }
  }

  function addGroup() {
    if (saving) return;
    const baseDraft = renaming ? applyRename(draft, renaming.id, renaming.value) : draft;
    const usedIDs = baseDraft.nodes.map((node) => node.id);
    const id = nextUniqueId("grp", usedIDs);
    const name = t("sidebarDefaultGroupName", { n: groups.length + 1 });
    const nextNodes: EditNode[] = [...baseDraft.nodes, { kind: "group", id, name, libraries: [] }];
    onChange({ ...baseDraft, nodes: nextNodes });
    setRenaming({ id, value: name });
    setAnnouncement(t("sidebarGroupAdded", { name }));
  }

  function deleteGroup(groupID: string) {
    if (saving) return;
    const target = draft.nodes.find(
      (node): node is Extract<EditNode, { kind: "group" }> =>
        node.kind === "group" && node.id === groupID,
    );
    if (!target) return;
    const nextNodes: EditNode[] = [];
    for (const node of draft.nodes) {
      if (node.kind === "group" && node.id === groupID) {
        for (const libID of node.libraries) nextNodes.push({ kind: "library", id: libID });
      } else {
        nextNodes.push(node);
      }
    }
    if (renaming?.id === groupID) setRenaming(null);
    pendingFocusAfterDelete.current = true;
    onChange({ ...draft, nodes: nextNodes });
    setAnnouncement(t("sidebarGroupDeleted", { name: target.name }));
  }

  function addDivider() {
    if (saving) return;
    const baseDraft = renaming ? applyRename(draft, renaming.id, renaming.value) : draft;
    const id = nextUniqueId(
      "div",
      baseDraft.nodes.map((node) => node.id),
    );
    onChange({ ...baseDraft, nodes: [...baseDraft.nodes, { kind: "divider", id }] });
    setAnnouncement(t("sidebarDividerAdded"));
  }

  function removeDivider(dividerID: string) {
    if (saving) return;
    const nextNodes = draft.nodes.filter(
      (node) => !(node.kind === "divider" && node.id === dividerID),
    );
    if (nextNodes.length === draft.nodes.length) return;
    pendingFocusAfterDelete.current = true;
    onChange({ ...draft, nodes: nextNodes });
    setAnnouncement(t("sidebarDividerRemoved"));
  }

  function onTopRowKeyDown(event: KeyboardEvent, index: number) {
    if (!event.altKey) return;
    const target = event.target as HTMLElement | null;
    if (target?.tagName === "INPUT" || target?.tagName === "SELECT") return;
    if (event.key === "ArrowUp") {
      event.preventDefault();
      moveTop(index, index - 1);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      moveTop(index, index + 1);
    }
  }

  function onGroupedRowKeyDown(event: KeyboardEvent, groupID: string, index: number) {
    if (!event.altKey) return;
    const target = event.target as HTMLElement | null;
    if (target?.tagName === "INPUT" || target?.tagName === "SELECT") return;
    if (event.key === "ArrowUp") {
      event.preventDefault();
      moveInGroup(groupID, index, index - 1);
    } else if (event.key === "ArrowDown") {
      event.preventDefault();
      moveInGroup(groupID, index, index + 1);
    }
  }

  function handleDropOnTopIndex(targetIndex: number) {
    if (!dragging || saving) return;
    if (dragging.kind === "top") {
      moveTop(dragging.index, targetIndex);
    } else {
      moveLibraryToGroup(dragging.libID, null, targetIndex);
    }
  }

  function handleDropOnGroup(targetGroupID: string, targetChildIndex?: number) {
    if (!dragging || saving) return;
    if (dragging.kind === "top") {
      if (dragging.nodeKind === "library") {
        moveLibraryToGroup(dragging.id, targetGroupID, targetChildIndex);
      } else {
        const topIndex = draft.nodes.findIndex(
          (node) => node.kind === "group" && node.id === targetGroupID,
        );
        if (topIndex >= 0) moveTop(dragging.index, topIndex);
      }
    } else if (dragging.groupID === targetGroupID) {
      if (targetChildIndex !== undefined) {
        moveInGroup(targetGroupID, dragging.index, targetChildIndex);
      }
    } else {
      moveLibraryToGroup(dragging.libID, targetGroupID, targetChildIndex);
    }
  }

  function clearDrag() {
    setDragging(null);
    setDropTarget(null);
  }

  function groupPicker(libraryID: string, title: string, currentGroupID: string) {
    if (!allowGrouping || !hasGroups) return null;
    return (
      <span
        className="bf-side-tool bf-side-group-picker"
        title={t("sidebarMoveToGroup", { name: title })}
      >
        <FolderInput aria-hidden className="size-3.5" />
        <select
          aria-label={t("sidebarMoveToGroup", { name: title })}
          value={currentGroupID}
          disabled={saving}
          onChange={(event) => moveLibraryToGroup(libraryID, event.target.value || null)}
        >
          <option value="">{t("sidebarTopLevel")}</option>
          {groups.map((group) => (
            <option key={group.id} value={group.id}>
              {group.name}
            </option>
          ))}
        </select>
      </span>
    );
  }

  function libraryControls(
    libraryID: string,
    title: string,
    index: number,
    siblingCount: number,
    onMove: (from: number, to: number) => void,
    currentGroupID: string,
  ) {
    const isPinned = !draft.hidden.has(libraryID);
    return (
      <>
        <span aria-hidden className="bf-side-grip" title={t("sidebarDragHint")}>
          <GripVertical className="size-4" />
        </span>
        <span className="bf-side-label">{title}</span>
        {groupPicker(libraryID, title, currentGroupID)}
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
          onClick={() => onMove(index, index - 1)}
        >
          <ChevronUp aria-hidden className="size-4" />
        </button>
        <button
          type="button"
          className="bf-side-tool"
          aria-label={t("sidebarMoveDown", { name: title })}
          aria-disabled={saving || index === siblingCount - 1}
          onClick={() => onMove(index, index + 1)}
        >
          <ChevronDown aria-hidden className="size-4" />
        </button>
      </>
    );
  }

  return (
    <div className={"bf-side-editor" + (hasGroups ? " has-groups" : "")}>
      <ul
        ref={listRef}
        className="bf-side-list"
        onDragOver={(event) => {
          if (dragging && event.target === listRef.current) event.preventDefault();
        }}
        onDrop={(event) => {
          if (!dragging || event.target !== listRef.current) return;
          event.preventDefault();
          handleDropOnTopIndex(dragging.kind === "top" ? topCount - 1 : topCount);
          clearDrag();
        }}
      >
        {draft.nodes.map((node, index) => {
          if (node.kind === "library") {
            const libraryID = node.id;
            const title = titleOf(libraryID);
            const isPinned = !draft.hidden.has(libraryID);
            const isDragging = dragging?.kind === "top" && dragging.id === libraryID;
            const isTarget = dropTarget?.kind === "top" && dropTarget.index === index;
            return (
              <li
                key={libraryID}
                className={
                  "bf-side-edit-row" +
                  (isPinned ? "" : " is-unpinned") +
                  (isDragging ? " is-dragging" : "") +
                  (isTarget ? " is-drop-target" : "")
                }
                draggable={!saving}
                onDragStart={(event) => {
                  if (saving) return;
                  setDragging({ kind: "top", index, nodeKind: "library", id: libraryID });
                  event.dataTransfer?.setData("text/plain", libraryID);
                  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                }}
                onDragOver={(event) => {
                  if (!dragging) return;
                  event.preventDefault();
                  setDropTarget({ kind: "top", index });
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  handleDropOnTopIndex(index);
                  clearDrag();
                }}
                onDragEnd={clearDrag}
                onKeyDown={(event) => onTopRowKeyDown(event, index)}
              >
                {libraryControls(libraryID, title, index, topCount, moveTop, "")}
              </li>
            );
          }

          if (node.kind === "divider") {
            const label = dividerLabel(node.id);
            const isDragging =
              dragging?.kind === "top" &&
              dragging.nodeKind === "divider" &&
              dragging.id === node.id;
            const isTarget = dropTarget?.kind === "top" && dropTarget.index === index;
            return (
              <li
                key={`divider:${node.id}`}
                className={
                  "bf-side-edit-row bf-side-edit-divider" +
                  (isDragging ? " is-dragging" : "") +
                  (isTarget ? " is-drop-target" : "")
                }
                draggable={!saving}
                onDragStart={(event) => {
                  if (saving) return;
                  setDragging({ kind: "top", index, nodeKind: "divider", id: node.id });
                  event.dataTransfer?.setData("text/plain", node.id);
                  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                }}
                onDragOver={(event) => {
                  if (!dragging) return;
                  event.preventDefault();
                  setDropTarget({ kind: "top", index });
                }}
                onDrop={(event) => {
                  event.preventDefault();
                  handleDropOnTopIndex(index);
                  clearDrag();
                }}
                onDragEnd={clearDrag}
                onKeyDown={(event) => onTopRowKeyDown(event, index)}
              >
                <span aria-hidden className="bf-side-grip" title={t("sidebarDragHint")}>
                  <GripVertical className="size-4" />
                </span>
                <hr aria-hidden="true" className="bf-side-edit-divider-rule" />
                <button
                  type="button"
                  className="bf-side-tool is-danger"
                  aria-label={t("sidebarRemoveDivider", { name: label })}
                  aria-disabled={saving || undefined}
                  onClick={() => removeDivider(node.id)}
                >
                  <X aria-hidden className="size-4" />
                </button>
                <button
                  type="button"
                  className="bf-side-tool"
                  aria-label={t("sidebarMoveUp", { name: label })}
                  aria-disabled={saving || index === 0}
                  onClick={() => moveTop(index, index - 1)}
                >
                  <ChevronUp aria-hidden className="size-4" />
                </button>
                <button
                  type="button"
                  className="bf-side-tool"
                  aria-label={t("sidebarMoveDown", { name: label })}
                  aria-disabled={saving || index === topCount - 1}
                  onClick={() => moveTop(index, index + 1)}
                >
                  <ChevronDown aria-hidden className="size-4" />
                </button>
              </li>
            );
          }

          const isRenaming = renaming?.id === node.id;
          const isDraggingGroup =
            dragging?.kind === "top" && dragging.nodeKind === "group" && dragging.id === node.id;
          const isDropInside = dropTarget?.kind === "group" && dropTarget.groupID === node.id;
          const isDropTop = dropTarget?.kind === "top" && dropTarget.index === index;

          return (
            <li
              key={`group:${node.id}`}
              className={
                "bf-side-edit-group" +
                (isDraggingGroup ? " is-dragging" : "") +
                (isDropInside ? " is-drop-inside" : "") +
                (isDropTop ? " is-drop-target" : "")
              }
              draggable={!saving && !isRenaming}
              onDragStart={(event) => {
                if (saving || isRenaming) return;
                setDragging({ kind: "top", index, nodeKind: "group", id: node.id });
                event.dataTransfer?.setData("text/plain", node.id);
                if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
              }}
              onDragOver={(event) => {
                if (!dragging) return;
                event.preventDefault();
                const draggingLibrary =
                  dragging.kind === "grouped" ||
                  (dragging.kind === "top" && dragging.nodeKind === "library");
                setDropTarget(
                  draggingLibrary ? { kind: "group", groupID: node.id } : { kind: "top", index },
                );
              }}
              onDrop={(event) => {
                event.preventDefault();
                handleDropOnGroup(node.id);
                clearDrag();
              }}
              onDragEnd={clearDrag}
              onKeyDown={(event) => onTopRowKeyDown(event, index)}
            >
              <div className="bf-side-edit-row bf-side-edit-group-header">
                {isRenaming ? (
                  <input
                    ref={renameInputRef}
                    type="text"
                    className="bf-side-rename-input"
                    aria-label={t("sidebarGroupNameLabel")}
                    value={renaming.value}
                    disabled={saving}
                    onChange={(event) =>
                      setRenaming((current) =>
                        current ? { ...current, value: event.target.value } : current,
                      )
                    }
                    onKeyDown={(event) => {
                      if (event.nativeEvent.isComposing) return;
                      if (event.key === "Enter") {
                        event.preventDefault();
                        commitRename(node.id, true);
                      } else if (event.key === "Escape") {
                        event.preventDefault();
                        event.stopPropagation();
                        cancelRename(node.id);
                      }
                    }}
                    onBlur={() => commitRename(node.id, false)}
                  />
                ) : (
                  <>
                    <span aria-hidden className="bf-side-grip" title={t("sidebarDragHint")}>
                      <GripVertical className="size-4" />
                    </span>
                    <span
                      className="bf-side-label"
                      onDoubleClick={() => {
                        if (!saving) {
                          setRenaming({ id: node.id, value: node.name });
                        }
                      }}
                    >
                      {node.name}
                    </span>
                    <button
                      type="button"
                      data-group-rename={node.id}
                      className="bf-side-tool"
                      aria-label={t("sidebarRenameGroup", { name: node.name })}
                      aria-disabled={saving || undefined}
                      onClick={() => {
                        if (!saving) {
                          setRenaming({ id: node.id, value: node.name });
                        }
                      }}
                    >
                      <Pencil aria-hidden className="size-3.5" />
                    </button>
                    <button
                      type="button"
                      className="bf-side-tool is-danger"
                      aria-label={t("sidebarDeleteGroup", { name: node.name })}
                      aria-disabled={saving || undefined}
                      onClick={() => deleteGroup(node.id)}
                    >
                      <Trash2 aria-hidden className="size-3.5" />
                    </button>
                    <button
                      type="button"
                      className="bf-side-tool"
                      aria-label={t("sidebarMoveUp", { name: node.name })}
                      aria-disabled={saving || index === 0}
                      onClick={() => moveTop(index, index - 1)}
                    >
                      <ChevronUp aria-hidden className="size-4" />
                    </button>
                    <button
                      type="button"
                      className="bf-side-tool"
                      aria-label={t("sidebarMoveDown", { name: node.name })}
                      aria-disabled={saving || index === topCount - 1}
                      onClick={() => moveTop(index, index + 1)}
                    >
                      <ChevronDown aria-hidden className="size-4" />
                    </button>
                  </>
                )}
              </div>
              <ul className="bf-side-edit-sublist">
                {node.libraries.length === 0 ? (
                  <li className="bf-side-edit-empty">{t("sidebarGroupEmpty")}</li>
                ) : (
                  node.libraries.map((libID, childIndex) => {
                    const title = titleOf(libID);
                    const isPinned = !draft.hidden.has(libID);
                    const isChildDragging =
                      dragging?.kind === "grouped" && dragging.libID === libID;
                    const isChildTarget =
                      dropTarget?.kind === "grouped" &&
                      dropTarget.groupID === node.id &&
                      dropTarget.index === childIndex;
                    return (
                      <li
                        key={libID}
                        className={
                          "bf-side-edit-row" +
                          (isPinned ? "" : " is-unpinned") +
                          (isChildDragging ? " is-dragging" : "") +
                          (isChildTarget ? " is-drop-target" : "")
                        }
                        draggable={!saving}
                        onDragStart={(event) => {
                          event.stopPropagation();
                          if (saving) return;
                          setDragging({
                            kind: "grouped",
                            groupID: node.id,
                            libID,
                            index: childIndex,
                          });
                          event.dataTransfer?.setData("text/plain", libID);
                          if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                        }}
                        onDragOver={(event) => {
                          event.stopPropagation();
                          if (!dragging) return;
                          event.preventDefault();
                          const draggingLibrary =
                            dragging.kind === "grouped" ||
                            (dragging.kind === "top" && dragging.nodeKind === "library");
                          setDropTarget(
                            draggingLibrary
                              ? { kind: "grouped", groupID: node.id, index: childIndex }
                              : { kind: "top", index },
                          );
                        }}
                        onDrop={(event) => {
                          event.stopPropagation();
                          event.preventDefault();
                          handleDropOnGroup(node.id, childIndex);
                          clearDrag();
                        }}
                        onDragEnd={(event) => {
                          event.stopPropagation();
                          clearDrag();
                        }}
                        onKeyDown={(event) => {
                          event.stopPropagation();
                          onGroupedRowKeyDown(event, node.id, childIndex);
                        }}
                      >
                        {libraryControls(
                          libID,
                          title,
                          childIndex,
                          node.libraries.length,
                          (from, to) => moveInGroup(node.id, from, to),
                          node.id,
                        )}
                      </li>
                    );
                  })
                )}
              </ul>
            </li>
          );
        })}
      </ul>
      {allowGrouping && (
        <div ref={addRef} className="bf-side-edit-add">
          <button
            type="button"
            className="bf-side-add-button"
            aria-disabled={saving || undefined}
            onClick={addGroup}
          >
            <Plus aria-hidden className="size-3.5" />
            <span>{t("sidebarNewGroup")}</span>
          </button>
          <button
            type="button"
            className="bf-side-add-button"
            aria-disabled={saving || undefined}
            onClick={addDivider}
          >
            <Plus aria-hidden className="size-3.5" />
            <span>{t("sidebarAddDivider")}</span>
          </button>
        </div>
      )}
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
          onClick={() => {
            const finalDraft = renaming ? applyRename(draft, renaming.id, renaming.value) : draft;
            if (finalDraft !== draft) onChange(finalDraft);
            setRenaming(null);
            onDone(finalDraft);
          }}
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
