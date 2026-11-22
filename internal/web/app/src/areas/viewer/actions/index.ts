// Item actions for the Viewer: mark watched, watchlist and rating, per Profile.
//
// Entry points for any surface (a card menu, an item page, the player):
//   <ItemActions target={itemTargetFor(item)} />        the ready-made controls
//   useItemActions(itemTargetFor(item), { enabled })    state + actions for a custom menu
//   useItemHide(target, title, { enabled })             Hide for this Profile (CORE-2836)
export {
  notifyEngagementChanged,
  readEngagementEpoch,
  useEngagementEpoch,
  useEngagementEvents,
} from "./engagementSync";
export { HiddenUndoNotice, HideWatchedToggle } from "./HiddenControls";
export type { HiddenItems, ItemHide } from "./hiddenItems";
export {
  hiddenKeyMoved,
  hideItem,
  unhideItem,
  useHiddenItems,
  useHiddenKey,
  useHideWatched,
  useItemHide,
} from "./hiddenItems";
export { ItemActions } from "./ItemActions";
export type { ItemActions as ItemActionsState } from "./useItemActions";
export { itemTargetFor, useItemActions } from "./useItemActions";
