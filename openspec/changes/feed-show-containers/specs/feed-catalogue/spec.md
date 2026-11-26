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
