## 1. Contract and data

- [ ] 1.1 Request `player-control` in `EmbeddedUIContract` and the web `CLIENT_CONTRACT`
- [ ] 1.2 Add player fields and operations to the web GraphQL types and fetchers (console, party, remote)

## 2. Console

- [ ] 2.1 Profiles: `player` kind on create, fixed kind label, controllers editor, walk-up switch with the D7 warning
- [ ] 2.2 Devices: bind and clear a Player Profile binding with a display name

## 3. Viewer

- [ ] 3.1 Play on picker lists player displays with presence, Private item and offline text, and commands them
- [ ] 3.2 `/web/play-on?display=` route resolves the QR handle; document the URL in `contracts/player-profile/v1/README.md`
- [ ] 3.3 HostLive PartyScreens offers available displays, adds them and makes a screen lead

## 4. Tests

- [ ] 4.1 Strings in every locale; vitest for fetchers, picker, route and console sections
- [ ] 4.2 Playwright coverage for the picker and the console
