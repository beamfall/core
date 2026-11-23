# Player Profile wire v1

Core owns the response shapes a Player Profile controller and display decode
(ADR-0240 D11): `PlayerBindingInfo`, `PlayerControllersInfo`,
`PlayerDisplayInfo`, `PartyAvailableDisplayInfo`, `PartyScreenInfo` and
`ControlRendererSessionResult`, plus the closed presence vocabulary
(`offline`, `ready`, `playing`).

Only clients that declare the `player-control` capability in the compatibility
handshake receive player display rows. An older controller sees an empty
`playerDisplays` and `availableDisplays`, and `controlRendererSession` or
`partyScreenAddDisplay` against a player display is refused as if the display
did not exist.

`internal/conformance/player_profile_wire_contract_test.go` decodes every
fixture strictly into core's Go types and requires a byte-identical re-encode.
Client kits vendor `fixtures.json` one way, byte for byte, and decode it with
their own SDK types under the same strictness. To change a shape, edit the Go
type and this file together, bump `contractVersion` and the registry version,
then refresh each consumer copy before core pins the new version.
