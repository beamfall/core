## MODIFIED Requirements

### Requirement: Scan groups files of one title into Versions

A scan SHALL attach a parsed, non-part, non-image file to the Media of another file from the same
directory and library when both parse to the same class, title, year, sequence and index, storing it
as a Version under the parser's version label. The other file MAY be one this scan imported or one an
earlier scan cataloged. When neither file carries a parsed version label, the scan SHALL label both
from the first of these that tells them apart and leaves both slots free: the resolution class of the
probed frame, the container, then the file name without its extension; the file that held the
default Version moves to its label. A file whose label is stored SHALL keep it on later scans. It
SHALL import the file as its own Media when no such slot is free or when the file's content already
belongs to another Media.

#### Scenario: Two resolutions of one title

- **WHEN** a directory holds `Night Harbor (2001) - 1080p.mkv` and `Night Harbor (2001) - 4K.mkv`
  and the parser labels them `1080p` and `4K`
- **THEN** the scan creates one Media with two Versions labelled `1080p` and `4K`

#### Scenario: Unlabelled file with a labelled sibling

- **WHEN** a directory holds `Coastline (1998).mkv` and `Coastline (1998) - Extended.mkv`
- **THEN** the scan creates one Media whose default Version is the unlabelled file and whose
  `Extended` Version is the other

#### Scenario: A file added beside a cataloged Version

- **WHEN** an earlier scan cataloged `Night Harbor (2001) - 4K.mkv` and `Night Harbor (2001) -
  1080p.mkv` is added to the same directory
- **THEN** the next scan adds the new file as the `1080p` Version of the existing Media

#### Scenario: Two unlabelled copies of one title

- **WHEN** a directory holds `Disclosure.Day.2026.REPACK.1080p.Remux.mkv`, probed at 1920x800, and
  `Disclosure.Day.2026.2160p.WEB-DL.mkv`, probed at 3840x1600, and the parser reports one title and
  year and no label for both
- **THEN** the scan creates one Media with two Versions labelled `1080p` and `2160p`, and a rescan
  keeps those labels

#### Scenario: Unprobed unlabelled copies

- **WHEN** two unlabelled copies of one title have no probed frame size and are `.mkv` and `.mp4`
- **THEN** their Versions are labelled `MKV` and `MP4`; two copies in one container are labelled by
  their file names

#### Scenario: Different titles stay apart

- **WHEN** two files in one directory parse to different titles or years
- **THEN** the scan creates one Media for each
