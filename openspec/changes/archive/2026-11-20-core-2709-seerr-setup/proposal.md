# Seerr setup handshake under the bounded admin window (CORE-2709)

## Why

Seerr 3.5.2 sets up a Jellyfin server in three steps. It signs in with `Users/AuthenticateByName`,
asks the server for a standing API key through `Auth/Keys`, and reads the server name. Against
Beamfall it fails at the first step: it reads `User.Name`, `User.ServerId` and
`User.Policy.IsAdministrator`, and Beamfall returns only `User.Id`. ADR-0171 §2.4 lets admin
credentials mint only a short setup session, never standing access, so `Auth/Keys` cannot mint a
durable key.

## What changes

- `Users/AuthenticateByName` returns the user's display name, the server ID, the server name and
  `Policy.IsAdministrator`, which is true only for the admin setup session. No other Profile data.
- The admin setup session carries a server-assigned `compat.setup` scope. Callers cannot request it.
- POST `Auth/Keys` (setup scope) returns 204 and creates nothing.
- GET `Auth/Keys` (setup scope) lists one key: the caller's own setup token, named after the
  `Client` parameter of its MediaBrowser authorization header. It expires with the setup session.
- Any other credential gets the uniform 401 on both `Auth/Keys` routes.

Seerr completes setup and stores the setup token as its API key. That key stops working when the
setup window ends, and the operator replaces it in Seerr's settings with a durable integration key
(a separate ticket). The post-setup library routes belong to CORE-2681.
