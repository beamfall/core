## ADDED Requirements

### Requirement: Subtitle stream title words set flags

The scan SHALL set a subtitle stream's hearing-impaired, forced and commentary flags from SDH or
CC, forced and commentary words in its container `title` tag when the stream's dispositions do not
already set them, and SHALL NOT set a flag from a word that "non", "not" or "no" negates.

#### Scenario: SDH named only in the title

- **WHEN** a muxed subtitle stream has the title "English (SDH)" and no hearing-impaired disposition
- **THEN** its caption track has the SDH flag

#### Scenario: Negated role

- **WHEN** a muxed subtitle stream has the title "English (non-SDH)"
- **THEN** its caption track has no SDH flag

### Requirement: Sidecar filename tags

Sidecar subtitle filename tags SHALL take the first language-shaped segment as the language,
skipping numeric segments, and SHALL read `_` between language and region as `-`.

#### Scenario: Numbered Brazilian Portuguese sidecar

- **WHEN** a sidecar is named `Movie.2.pt_BR.forced.srt`
- **THEN** its language is `pt-BR` and it is forced

### Requirement: Subtitle labels name language, region and flags

The web Subtitles picker and the player caption menu SHALL label a track by its language display
name including region or script, followed by its forced, SDH, commentary and described flags. Format and source SHALL be added only
where they tell tracks with the same label apart, and a number only when two tracks would still
share a label.

#### Scenario: Regional and flagged tracks

- **WHEN** a file has tracks tagged `es-419`, `zh-Hant` and English with the SDH flag
- **THEN** they are labelled "Spanish (Latin America)", "Chinese (Traditional)" and "English · SDH"

#### Scenario: Identical tracks

- **WHEN** two English tracks have no title and no flags
- **THEN** their labels differ only by a track number

### Requirement: Subtitle tracks are ordered by preference and language

The picker SHALL list Auto and Off first, then the tracks in the profile's subtitle languages in
preference order (the display locale's language when the profile has none), then the other tracks
by language display name, then tracks with no language. Each language's tracks SHALL stay together,
including regions other than a preferred region, forced first, then regular, then SDH.

#### Scenario: Preferred language first

- **WHEN** the profile prefers Spanish and the file has French, English and two Spanish tracks, one SDH
- **THEN** the list is Auto, Off, Spanish, Spanish · SDH, English, French
