# Viewer pairing-code switch

Ticket: CORE-0393 (`queue:core:main`, PAIRING-DISABLE-VIEWER-CODES-1).

## Why

An operator who pairs every device through a Manager, or who wants to stop casual Viewer
enrollment for a while, has no way to turn Viewer pairing codes off. Remote enrollment already
has an operator switch; Viewer codes do not.

## What Changes

- A new persisted Console setting, `viewerPairingCodesDisabled`, default off, read and written
  through `consoleConfig` / `setConsoleConfig`.
- While it is on, minting a Viewer-role pairing code (Console and owner paths) is refused with
  `ErrForbidden` and writes nothing. Redeeming an outstanding Viewer code minted before the switch
  was turned on fails exactly like an invalid code, so the switch reveals nothing to the device.
- Each refusal is recorded in the activity log as `enroll.denied` against a `pairing_policy`
  target, naming the mint or redeem path.
- Manager-role codes, Manager promotion and demo enrollment are unaffected.
- The Console Config section gains the switch with help text saying Manager pairing and demo
  enrollment stay enabled.

## Impact

`internal/store` (console config), `internal/pairing`, `internal/graph` (console config field and
argument), the web Console config section. Additive wire field and argument, published in the
next additive wire-contract minor. No migration.
