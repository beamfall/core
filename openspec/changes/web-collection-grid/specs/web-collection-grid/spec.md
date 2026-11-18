## ADDED Requirements

### Requirement: Collection poster grid

The web viewer's `/collections` page SHALL show each collection as a card with a collage of up to
four of its visible members' posters, its name and its item count. The member posters SHALL come
through the collection feed so they obey the same visibility rules as the collection page.

#### Scenario: Collection with members

- **WHEN** a viewer opens `/collections` and a collection has five visible members with posters
- **THEN** its card shows the first four members' posters, its name and "5 items"

#### Scenario: Empty collection

- **WHEN** a collection has no visible members with posters
- **THEN** its card shows a placeholder instead of an image
