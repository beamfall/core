// Item actions for the Viewer: mark watched, watchlist and rating, per Profile.
//
// Entry points for any surface (a card menu, an item page, the player):
//   <ItemActions target={itemTargetFor(item)} />        the ready-made controls
//   useItemActions(itemTargetFor(item), { enabled })    state + actions for a custom menu
export { useEngagementEpoch, useEngagementEvents } from "./engagementSync";
export { ItemActions } from "./ItemActions";
export type { ItemActions as ItemActionsState } from "./useItemActions";
export { itemTargetFor, useItemActions } from "./useItemActions";
