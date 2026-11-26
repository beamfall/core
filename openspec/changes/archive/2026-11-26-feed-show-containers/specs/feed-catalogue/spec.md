## ADDED Requirements

### Requirement: A feed Subscription publishes a show container with artwork

Polling a feed SHALL create or update one container Entity for the Subscription when the Library's
plugin declares an outermost container kind that holds Media through a single-holder relation, and
SHALL hold the feed's catalogue episodes through that relation.

#### Scenario: Show with channel art

- **WHEN** a feed with a title and an `itunes:image` is polled into such a Library
- **THEN** a container Entity titled by the feed exists with the image as its poster and each
  episode is held by it

#### Scenario: Episode artwork

- **WHEN** an episode has its own `itunes:image`
- **THEN** it is the episode's poster and thumb; an episode without one takes the show's poster

#### Scenario: Plugin without a container kind

- **WHEN** the Library's plugin declares no such kind
- **THEN** no Entity or artwork is written

### Requirement: Catalogue insertion order is newest-last

Catalogue entries SHALL be inserted in ascending publish-date order (ties keep feed order) so
the `recently-added` source, which reads newest row first, lists the newest episode first. The
entries a call returns SHALL stay in feed order.

#### Scenario: newest episode lists first
- **WHEN** a feed with five dated entries is catalogued
- **THEN** the newest entry's media row sorts ahead of the oldest in `recently-added`
