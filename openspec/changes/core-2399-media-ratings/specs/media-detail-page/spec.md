## MODIFIED Requirements

### Requirement: Facts row

Under the title the page SHALL show a row of facts: the year, the runtime when the opening card
carried a duration of at least a minute, the content rating, the community score, and the titles of
up to three distinct credits whose kind has the category archetype. The content rating SHALL read
the `certification-display` field, falling back to `certification`, and SHALL render as a bordered
badge. The community score SHALL read the `community-rating` field on a 0 to 10 scale, render with
one decimal, show only when it and `community-rating-votes` are both above zero, and carry an
accessible name stating the field's label, the score out of 10 and the vote count. These fields
SHALL NOT also appear in the field list. Unknown facts SHALL be omitted.

#### Scenario: Facts from server and card

- **WHEN** the item has year 2001, a rating "PG-13", one category credit "Drama" and the card
  carried a duration of 5400 seconds
- **THEN** the facts row reads "2001", "1 h 30 min", "PG-13", "Drama" in that order

#### Scenario: Display rating and community score

- **WHEN** the item stores `certification-display` "TV-MA", `community-rating` 7.25 labelled
  "TMDB rating" and `community-rating-votes` 1200
- **THEN** the facts row shows a "TV-MA" badge and a score "★ 7.3" named "TMDB rating 7.3 out of
  10, 1,200 votes"

#### Scenario: No votes

- **WHEN** the item stores `community-rating` 0 and `community-rating-votes` 0
- **THEN** no score is shown
