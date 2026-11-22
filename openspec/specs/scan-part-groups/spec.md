# scan-part-groups Specification

## Purpose
Group the disc subfolders of a single book into one Media during scanning.

## Requirements

### Requirement: Disc subfolders of a book join one Media
Scan SHALL import the Parts a parser keys to one part group as one Media even when files that
are not Parts of that group sort between them, provided those files lie directly in the group's
folder. Scan SHALL order the Parts folder by folder, each folder placed by its lowest part
number with ties going to the folder walked first, and within a folder by part number with ties
going to the file walked first. Equal part numbers SHALL NOT fail the scan.

#### Scenario: Loose file between disc folders
- **WHEN** a book folder holds `CD1/01.mp3`, `CD1/02.mp3`, `Credits.mp3`, `Disc 2/01.mp3` and `Disc 2/02.mp3`
- **THEN** the four disc tracks are one Media in disc-then-track order and `Credits.mp3` is not one of its Parts

#### Scenario: Two folders name the same disc
- **WHEN** a book folder holds `CD1/01.mp3`, `CD1/02.mp3`, `Disc 01/01.mp3` and `Disc 01/02.mp3`
- **THEN** the scan succeeds and the four tracks are one Media ordered `CD1/01`, `CD1/02`, `Disc 01/01`, `Disc 01/02`

#### Scenario: Flat numbered group
- **WHEN** a folder holds `1 - Opening.mp3`, `2 - Middle.mp3` and `10 - Ending.mp3`
- **THEN** they are one Media ordered 1, 2, 10, as before
