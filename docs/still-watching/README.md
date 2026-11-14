# "Are you still watching?" (CORE-1689)

After a run of items that started on their own, every Beamfall player stops
and asks whether anyone is still there before it starts the next one. This
keeps autoplay from running all night, and keeps progress and "watched"
state honest.

The behaviour is the same on every client: web, iPhone, Apple TV, the macOS
Viewer and Manager players, Android phone and Android TV.

## The setting

One per-Profile value in the generic client-preference store
(`profile_client_prefs`, ADR-0014), read and written with the existing
`clientPrefs` / `setClientPref` graph operations. There is no new schema, no
migration and no dedicated graph field, the same as the `autoskip.*` keys
(`internal/graph/graph_autoskip.go`). The server only stores the value; it
never acts on it.

| Key | Value | Meaning |
|---|---|---|
| `stillwatching.after` | `1` … `10` | Ask after this many items in a row started on their own |
| | `off` | Never ask |
| | absent, empty or anything else | The default, **3** |

The web Profile settings page edits it. Every client reads the same value, so
one change applies on every device the Profile uses.

## The rule

- **Streak.** A player counts the items that started on their own, in a row:
  the Up Next countdown ran out, or the item ended and the next one started
  without anyone pressing anything.
- **Input resets it.** The streak returns to 0 when the viewer starts
  something themselves (picks an item, presses Play Now on Up Next, presses
  Continue on the prompt) or touches the player in any way: a tap, click,
  key, remote button, seek, pause or skip.
- **The prompt.** When an item ends, a next item is queued, and the streak has
  reached N, the player does not start the next item. Playback stays stopped
  and the player shows:
  - title "Are you still watching?"
  - the next item's title
  - **Continue watching**: starts the next item and resets the streak to 0
  - **Stop**: closes the prompt and does whatever the player does at the end
    of an item with nothing queued
- While the streak has reached N, the Up Next countdown is not shown, since it
  would count down to an autoplay that will not happen.
- With nothing queued there is no autoplay, so there is no prompt.
- Only the Up Next countdown path counts. A player that advances straight to
  the next item on end with no countdown (the web player's audio path, a
  continuous audio queue) is exempt: it neither counts towards the streak nor
  prompts.
- The streak lives in memory for the running app. It is not synced and does
  not survive a restart.

The words above are domain-neutral on purpose: an "item" is whatever the queue
holds next. No client names a media kind in this rule.

## Example

N = 3. The viewer picks item 1. Items 2, 3 and 4 start on their own (streak 3).
When item 4 ends, the player asks instead of starting item 5.
