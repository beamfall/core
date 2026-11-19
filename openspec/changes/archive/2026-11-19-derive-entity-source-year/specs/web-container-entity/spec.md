## MODIFIED Requirements

### Requirement: Browse catalog shelves are ranked

A browse Layout's shelf Sections over `entity-items` or `all-items` SHALL be ordered on the client:
items in progress first, then unstarted, then finished, newer year first within each tier, then
the server's order. Grids and See All SHALL keep the server's order.

For an Entity card returned by a Section Source, the server SHALL populate the existing year
field with the earliest positive year among directly linked, untrashed Media allowed by the
request's Library and certification policy. The field SHALL be absent when no such year exists.

#### Scenario: An in-progress container leads the shelf

- **WHEN** a shelf lists an unstarted, a finished and an in-progress container in title order
- **THEN** it renders the in-progress, the unstarted, then the finished one

#### Scenario: Newer unstarted containers appear first

- **GIVEN** two unstarted containers whose earliest visible linked Media years differ
- **WHEN** a catalog shelf receives the older container first in the Source response
- **THEN** the newer container renders first
