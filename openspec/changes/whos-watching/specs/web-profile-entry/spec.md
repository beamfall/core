## ADDED Requirements

### Requirement: A Profile carries an avatar token

Core SHALL store an optional avatar token per Profile as the reserved client preference
`profile.avatar`, and the `profiles` query SHALL return it as `avatar`, empty when none is chosen.
Writing the preference SHALL accept only a token of 1 to 48 characters drawn from lowercase
letters, digits, `.` and `-`, starting with a letter or digit; any other value SHALL be refused and
store nothing. An empty value SHALL clear the choice. Core SHALL NOT interpret the token.

#### Scenario: A Profile chooses an avatar

- **WHEN** a session sets `profile.avatar` to `rocket.3`
- **THEN** the `profiles` query returns `avatar` `rocket.3` for that Profile and empty for the others

#### Scenario: A malformed avatar is refused

- **WHEN** a session sets `profile.avatar` to a value containing a URL or markup
- **THEN** the write is refused and the stored avatar is unchanged

#### Scenario: A Profile clears its avatar

- **WHEN** a session sets `profile.avatar` to the empty string
- **THEN** the `profiles` query returns an empty `avatar` for that Profile

### Requirement: The web picker shows avatars and asks for a PIN in place

The web profile picker SHALL show every available Profile as an avatar tile with its name, using a
generated avatar when no recognised token is stored. Choosing an adult Profile other than the
current one SHALL ask for its PIN on the picker itself and switch only when the server accepts it.
Choosing a managed Profile SHALL switch without a prompt.

#### Scenario: Switching to an adult Profile

- **WHEN** a person chooses another adult Profile and enters its PIN
- **THEN** the session switches to that Profile and the viewer opens

#### Scenario: Wrong PIN

- **WHEN** a person enters a PIN the server refuses
- **THEN** the picker says the PIN was not accepted, clears the field and stays on the prompt

#### Scenario: Throttled attempts

- **WHEN** the server reports that PIN attempts are throttled
- **THEN** the picker says to wait and try again, and stays on the prompt

#### Scenario: Generated avatar

- **WHEN** a Profile has no recognised avatar token
- **THEN** it shows the same generated avatar on every visit

#### Scenario: Switching to a managed Profile

- **WHEN** a person chooses a managed Profile
- **THEN** the session switches without asking for a PIN

### Requirement: The picker is shown on entry and is one click from the header

When more than one Profile is available, the web viewer SHALL show the picker the first time a
browser tab opens the viewer, and SHALL then continue to the page the person asked for. With one
Profile it SHALL NOT interrupt. The viewer header SHALL show the current Profile's avatar as a link
that opens the picker.

#### Scenario: Entering with several Profiles

- **WHEN** a browser tab first opens a viewer page and two Profiles exist
- **THEN** the picker is shown, and after a choice the person lands on the page they opened

#### Scenario: Entering with one Profile

- **WHEN** a browser tab first opens a viewer page and one Profile exists
- **THEN** the page opens directly

### Requirement: The signed-out page says how to sign in

The signed-out page SHALL say in plain words where a pairing code comes from: from a device already
signed in as the owner, from the `beamfall pairing-code` command on the server computer, or from
the person who runs the server. It SHALL keep the pairing-code form. It SHALL show another sign-in
route only when the server reports that route enabled.

#### Scenario: Pairing code is the only route

- **WHEN** an unenrolled browser opens the signed-out page on a server with no other sign-in route
  enabled
- **THEN** it sees the pairing-code form and the three places a pairing code comes from, and no
  other sign-in form
