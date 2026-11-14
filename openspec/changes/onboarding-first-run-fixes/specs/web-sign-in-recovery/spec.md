## ADDED Requirements

### Requirement: A rebindable LAN hostname refusal says how to proceed

When admin sign-in or first-run setup is refused because the browser reached the server by a
rebindable LAN hostname over plain HTTP, the refusal SHALL carry the code `lan_hostname_not_allowed`.
For a browser on the same LAN it SHALL also carry the server's IP URL(s) in `lanUrls`, derived from the
address that accepted the connection. No URL SHALL be offered over TLS, through a proxy or relay, or on
a subnet the browser cannot reach. The refusal itself SHALL be unchanged.

#### Scenario: LAN browser used a hostname

- **WHEN** a browser on the server's subnet signs in as admin through a rebindable hostname over HTTP
- **THEN** the answer is `403` with `code` `lan_hostname_not_allowed` and the server's IP URL in
  `lanUrls`, and the page links it

#### Scenario: Browser not on the LAN

- **WHEN** the same refusal is sent to a browser that is not on a shared subnet
- **THEN** `lanUrls` is empty

### Requirement: The enroll page shows its recovery paths

The enroll page SHALL show admin-password sign-in as a visible action beneath the code form when the
sign-in mode allows it, and in Strict mode SHALL say the pairing code is obtained by running the
`pairing-code` command on the server. Signing out SHALL warn beforehand that the browser will be
forgotten and a new pairing code is needed.

#### Scenario: Home mode

- **WHEN** the enroll page loads in Home mode
- **THEN** admin-password sign-in is visible without expanding anything

#### Scenario: Strict mode

- **WHEN** the enroll page loads in Strict mode
- **THEN** it shows how to get a pairing code on the server and no password form

#### Scenario: Sign-out

- **WHEN** the account menu is open
- **THEN** text next to Sign out says the browser will be forgotten
