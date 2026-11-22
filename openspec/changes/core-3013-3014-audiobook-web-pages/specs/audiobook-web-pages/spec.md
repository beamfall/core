## ADDED Requirements

### Requirement: Browse rules filter by a relation attribute value
An Entity browse source MAY name a relation attribute and a value. The source SHALL then match
only relations whose attribute lists that value among its comma-separated values. The audiobook
Person browse SHALL list only people credited as author.

#### Scenario: Authors grid
- **WHEN** one person is credited as author, one as narrator and one as "author, narrator"
- **THEN** the audiobook Person browse lists the author and the author-narrator only

### Requirement: Book page shows position, time left and chapters
For an audio Media in a relation-sequenced container, the book page SHALL show its position from
the relation's sequence. With saved progress it SHALL offer Resume with the time left, and SHALL
list the Media's chapters with the current chapter marked.

#### Scenario: Book in progress
- **WHEN** a book with sequence "4.5" in a series has saved progress and chapter markers
- **THEN** the page shows its position in the series, Resume with time left, and the chapter
  holding the saved position is marked current

### Requirement: Person and series pages follow the relations
The person page SHALL group a person's audio Media by credit role. The series page SHALL list
its books in sequence order with per-book progress, and its Play SHALL start one book without a
play queue.

#### Scenario: Author who narrates
- **WHEN** a person wrote one book and narrated another
- **THEN** the person page shows the books under Wrote and Narrated respectively

#### Scenario: Series play
- **WHEN** a listener plays from a series page and the book ends
- **THEN** playback stops; the next book does not start

### Requirement: Next in series is manual
The book page and the player's finish state SHALL offer the next book in sequence only after the
current book is finished. The offer SHALL be a link the listener follows; it SHALL NOT be queued,
sent as the next Media or started automatically. The next-up Source SHALL pick the first
unfinished book in sequence order, so a half-step sequence sorts between its neighbours.

#### Scenario: Finished book two
- **WHEN** books 1 and 2 of a series are finished
- **THEN** the next-up Source yields book 3 and nothing plays until the listener chooses it

#### Scenario: Half-step sequence
- **WHEN** books 1 to 4 are finished and the series has books 4.5 and 5
- **THEN** the next-up Source yields book 4.5
