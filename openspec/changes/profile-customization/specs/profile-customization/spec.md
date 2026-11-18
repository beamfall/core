## ADDED Requirements

### Requirement: Managers set the Profile order every picker uses

The server SHALL keep a manager-chosen order of Profiles and return Profiles in that order from
every listing. Setting it SHALL need an Admin Session and SHALL name every live Profile exactly
once.

#### Scenario: Reordering Profiles

- **WHEN** an Admin Session calls `reorderProfiles` with every live Profile ID in a new order
- **THEN** the mutation returns the Profiles in that order
- **AND** the `profiles` query returns them in that order
- **AND** an Activity entry `profile.order` is recorded

#### Scenario: Refused reorder

- **WHEN** `reorderProfiles` is called without an Admin Session, or names an unknown Profile
- **THEN** it returns an error and the order is unchanged

### Requirement: Managers set a Profile's icon and colour

An Admin Session SHALL be able to set any Profile's avatar token with `setProfileAvatar`, using
the same token format a Profile sets for itself.

#### Scenario: Setting another Profile's colour

- **WHEN** an Admin Session calls `setProfileAvatar` for another Profile with a valid token
- **THEN** that Profile's `avatar` is the token on every device
- **AND** an Activity entry `profile.avatar` is recorded

### Requirement: Profiles have an uploaded picture shown on every device

The server SHALL accept a JPEG or PNG picture for a Profile, store only its own normalized copy
(EXIF orientation applied, centre square, at most 512 px, opaque, no metadata) and serve it at a
versioned `pictureURL`. Clients SHALL show the picture in place of the avatar when `pictureURL`
is set.

#### Scenario: Uploading a picture

- **WHEN** a Profile uploads a PNG for itself
- **THEN** `pictureURL` becomes `/api/profiles/picture/<id>?v=<version>`
- **AND** fetching it returns a square JPEG with an immutable cache header
- **AND** an Activity entry `profile.picture` is recorded

#### Scenario: Who may change a picture

- **WHEN** a Profile without an Admin Session uploads a picture for another Profile
- **THEN** the server refuses with 403
- **AND** an Admin Session may change any Profile's picture

#### Scenario: Unsupported input

- **WHEN** the upload is not a JPEG or PNG, or is over 16 MiB
- **THEN** the server refuses it and keeps the previous picture

#### Scenario: Removing a picture

- **WHEN** the picture is deleted
- **THEN** `pictureURL` is empty and devices show the avatar again
