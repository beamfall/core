## ADDED Requirements

### Requirement: Add a device shows a QR code for the enroll page

After a pairing code is minted, Console "Add a device" SHALL show a QR code that encodes the
enroll URL at an address a phone on the same network can open, together with that URL as text. It
SHALL NOT encode a loopback address or the pairing code.

#### Scenario: Console opened at a network address

- **WHEN** the owner mints a pairing code in a Console opened at `http://192.168.1.10:2326/web/`
- **THEN** the panel shows a QR code encoding `http://192.168.1.10:2326/web/enroll`
- **AND** shows that URL as text

#### Scenario: Console opened on loopback

- **WHEN** the owner mints a pairing code in a Console opened at `http://localhost:2326/web/`
- **AND** the server reports a LAN endpoint `http://192.168.1.10:2326`
- **THEN** the QR code encodes `http://192.168.1.10:2326/web/enroll`, not a `localhost` URL

#### Scenario: Loopback with no LAN endpoint

- **WHEN** the Console is on loopback and the server reports no LAN endpoint
- **THEN** no QR code is shown
- **AND** the panel says to open the Console at the server's network address to get one

#### Scenario: The pairing code stays out of the QR

- **WHEN** a QR code is shown for a minted pairing code
- **THEN** the encoded URL does not contain the pairing code
