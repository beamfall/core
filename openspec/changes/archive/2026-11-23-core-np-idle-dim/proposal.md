# Web Now Playing: idle controls recede

Ticket: CORE-3351 (`queue:core:main`). Reverses the CORE-1637 exemption of the audio stage from
the player's idle chrome auto-hide.

## Why

The full-screen music Now Playing view keeps the Back / Watch Party bar, the visual selector, the
waveform scrubber and the transport row, and the album art, at full brightness for as long as a
track plays. Nobody is using them, and on a living-room display they are the brightest thing on
screen. The owner asked for them to recede when idle.

## What Changes

- The audio stage uses the video player's idle timer (`useChromeAutoHide`, 3 s): no pointer move,
  press, key or focus while playing marks it idle.
- Idle on the audio stage dims instead of hiding: the transport and the top bar go to 15 %
  opacity, the artwork, words and listening controls to 45 %. Open lyrics are not dimmed.
- Activity brings everything back with a 200 ms fade; the dim itself fades over 800 ms.
- Nothing dims while paused or while a transport menu, popover or the Watch Party panel is open
  (already true of the timer), nor while the pointer is over a control region or a control has
  keyboard focus (`:hover` / `:focus-visible`).
- Reduced motion: the opacity changes with no transition.
- Ambient (OLED-safe) mode keeps its own 45 s idle; video keeps hiding its chrome as before.

## Impact

Web only: `PlayerPage.tsx`, `player-deferred.css`, `chrome.css`, a vitest.
