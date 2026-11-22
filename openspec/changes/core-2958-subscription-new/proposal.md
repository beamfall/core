## Why

Ticket: CORE-2958 in `queue:core:main` (podcasts P0). ADR-0238 (feed subscriptions) and the
podcasts design (§4.1, §7.1, NEW-PC06) are the binding design.

A Profile that follows Subscriptions needs one place that shows what arrived since it followed,
and a per-Subscription choice to have new entries go straight into its Play Queue. Today the
catalogue records every feed entry as a Media (CORE-2953) but nothing tells a Profile which of
them are new to it.

## What Changes

- A generic, Profile-scoped Source kind **`subscription-new`**: unplayed Media from the
  Subscriptions the Profile follows, published after the Profile followed, newest first. Marking a
  Media played removes it. The Source applies the caller's Library, Reveal, certification and
  Mute policy like every other Profile Source, and is empty without a Profile.
- A per-follow **new-entry route**: `new` (default, Source only), `queue-end` or `queue-front`.
  It is stored on the Profile's follow row (events migration 0028) and set with the session
  mutation `setFollowNewEntryRoute(subscriptionID, route)`. `SubscriptionInfo` and `FollowInfo`
  report the caller's route.
- **Routing**: after a poll catalogues new entries, each follower whose route is `queue-end` or
  `queue-front` gets the entries published after its follow added to its Play Queue, oldest
  first, under that Profile's own Library and certification policy. A refusal (a managed access
  window, a disabled Profile) skips that Profile and is logged without failing the poll. The first
  poll's back catalogue is never routed.

## Out of scope

- Plugin manifests cannot name `subscription-new` until the plugin SDK's closed Source vocabulary
  adds it (an SDK release; filed as a follow-up ticket). Clients reach it through `sourceItems`.
