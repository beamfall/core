## ADDED Requirements

### Requirement: Server locale negotiation

The server SHALL resolve one locale per request from the shipped locales in its vendored locale
declaration, preferring a profile preference when one is known, then the request's
`Accept-Language` header, then the source locale `en`.

#### Scenario: Regional Accept-Language tag

- **WHEN** a request carries `Accept-Language: pt-BR,pt;q=0.9,en;q=0.5`
- **THEN** server-localized text in the response is rendered in `pt`

#### Scenario: Unsupported language

- **WHEN** a request carries `Accept-Language: ja` or no header
- **THEN** server-localized text in the response is rendered in `en`

#### Scenario: Excluded language

- **WHEN** a request carries `Accept-Language: fr;q=0, de`
- **THEN** server-localized text in the response is rendered in `de`

### Requirement: Server-localized text comes from catalogs

Text the server renders for people (class b) SHALL come from a catalog entry that every shipped
locale translates with the same placeholders. A missing entry in an `in-progress` locale SHALL fall
back to the source locale per key.

#### Scenario: Notification title

- **WHEN** a French-language client lists the notification inbox
- **THEN** a watch party invitation's title is the French catalog text, not the stored English title

#### Scenario: Library folder check

- **WHEN** an admin with `Accept-Language: de` adds a library folder that does not exist
- **THEN** the GraphQL error message is the German catalog sentence naming the folder, and its
  `extensions.code` is still `INVALID`

### Requirement: Stable codes stay stable

Server messages that clients branch on (class a) SHALL keep their documented code and their message
text unchanged, whatever the request locale.

#### Scenario: Coded GraphQL error

- **WHEN** a request with `Accept-Language: es` fails with `reveal attempts throttled`
- **THEN** the message stays `reveal attempts throttled` and `extensions.code` is `THROTTLED`

### Requirement: New user-facing server text is guarded

A conformance test SHALL fail when a server notification title, manifest locale string or
sentence-style user message is added as an English literal instead of a catalog key, or when a
catalog key is missing from a shipped locale.

#### Scenario: New English notification title

- **WHEN** a change creates a notification with a literal English title
- **THEN** the conformance test fails and names the file and line
