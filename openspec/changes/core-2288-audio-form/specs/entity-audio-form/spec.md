## ADDED Requirements

### Requirement: An Entity read carries an audio-form hint for audio containers

The `entity(id)` read SHALL set `audioForm` only for an Entity of the container archetype whose
first member Media, in relation order, is audio. It SHALL be `feed`, with `subscriptionID` set to
that Subscription, when the member is a catalogued Subscription entry; otherwise `long-form` when
the Library's plugin resumes Media; otherwise it SHALL be omitted. The hint SHALL be derived
from platform data only, with no plugin-specific vocabulary.

#### Scenario: A followed feed's container reads feed

- **WHEN** a container Entity's first member is an audio Subscription entry
- **THEN** `audioForm` is `feed` and `subscriptionID` names the Subscription

#### Scenario: A resumable audio container reads long-form

- **WHEN** a container Entity's first member is audio, not a Subscription entry, and the Library's plugin resumes Media
- **THEN** `audioForm` is `long-form` and `subscriptionID` is omitted

#### Scenario: Other Entities carry no hint

- **WHEN** the Entity is not a container, has no members, its first member is video, or its plugin does not resume Media
- **THEN** `audioForm` and `subscriptionID` are omitted
