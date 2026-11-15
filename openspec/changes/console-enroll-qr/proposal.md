# Add a device: QR code for the enroll page

Tickets: CORE-1614 (`queue:core:main`)

## Why

Console "Add a device" shows a pairing code and the enroll address, but the owner still has to type
that address on a phone. A QR code lets the phone's camera open the enroll page directly.

## What changes

- The minted-code panel on Console "Add a device" shows a QR code that encodes the enroll URL
  (`<address>/web/enroll`), with the same URL shown as text beside it.
- The address is the one the Console is open at. On loopback (`localhost`, `127.0.0.0/8`, `::1`)
  it is the server's first LAN endpoint from `remoteAccess.endpoints`, which the Console already
  reads. With no LAN endpoint, the panel explains how to get a QR code instead of encoding
  `localhost`.
- The pairing code is never encoded: it is a credential, so the person still types it.

## Encoder decision

The web app gets its own small QR encoder (`internal/web/app/src/lib/qr/encode.ts`): byte mode,
error-correction level M, the smallest fitting version, and the lowest-penalty mask. It adds no
npm dependency, so there is no licence or size review, and it is the app's single QR encoder.
CORE-1187 (theme export as a QR) reuses it. Its output was checked against an independent reader
(macOS CoreImage) at versions 1 to 26, and a golden-matrix test pins one enroll URL.

## Non-goals

- No server or wire-contract change: LAN endpoints already come from `remoteAccess`.
- QR scanning or pairing in native clients.
