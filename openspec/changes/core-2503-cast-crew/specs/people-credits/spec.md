## ADDED Requirements

### Requirement: Hydration writes credit attributes in billing order

Identification SHALL write the relation attributes a scraper's EntityMap resolves for an element
on the Entity's relation row to the Media, and SHALL use the map's ordinal (plus its offset) as
the row's sort order, so `media(id).credits` lists credits in the provider's billing order.

#### Scenario: Cast billing and character

- **WHEN** a scraper returns cast elements with `character` and billing `order` values 1 then 0
- **THEN** the credit billed 0 is listed first and each credit carries its character

### Requirement: Repeated links merge into one credit

Two or more links of one Entity to the Media under one relation SHALL merge into one relation row
whose attribute values are the distinct values joined with ", " in order and whose sort order is
the smallest. An Entity hydrated without typed fields SHALL NOT erase fields already stored for it.

#### Scenario: Director who also acts

- **WHEN** one person appears in the cast as "Herself" and in the crew as Director
- **THEN** the Media has one credit for that person carrying `character` "Herself", `job`
  "Director" and the cast's sort order

#### Scenario: Name-only credit keeps a stored headshot lookup

- **WHEN** a person's fields were stored by an earlier lookup and a later identification credits
  them past the lookup cap
- **THEN** the person's stored fields are unchanged

### Requirement: Web detail names directors and writers

The web Media detail page SHALL name the people credited with job "Director" and those credited
with job "Writer" or "Screenplay", each linked to the person page, in credit order and once each.
Neither line SHALL be shown when no credit holds that job.

#### Scenario: Film with a director and a writer

- **WHEN** a film credits Ron Howard as Director and Akiva Goldsman as Screenplay
- **THEN** the detail page shows a director line linking to Ron Howard and a writer line linking
  to Akiva Goldsman

### Requirement: Web Cast & Crew view

The web Media detail page SHALL show only the first group of the cast row, at most 20 people,
and SHALL offer "See all" when credits are left out. "See all" SHALL open a Cast & Crew page for
the item listing every person credit grouped by department in credit order, cast first. A credit
carrying both a character and a job SHALL appear in the cast with the character and in each of
its departments with the job.

#### Scenario: Lead first with characters

- **WHEN** a film's person credits are its billed cast followed by crew with departments
- **THEN** the detail page's People row lists the cast, lead first, with each character, and a
  "See all" link

#### Scenario: Everyone grouped by department

- **WHEN** the viewer opens "See all"
- **THEN** the page lists the cast, then each crew department under its name, with each person's
  job
