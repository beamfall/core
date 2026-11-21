## ADDED Requirements

### Requirement: A disc stream file beside a titled file is an Extra

The scan SHALL treat a file as an Extra when its name is a five-digit disc stream number with an
`.m2ts` or `.mts` extension, its folder is strictly below the Library root, and that folder also
holds a video file that is neither a disc stream file nor a suffixed Extra. The Extra SHALL link to
the sole main Media of its folder. The scan, Extra linking and the estimate SHALL share this rule.

#### Scenario: Stream file beside the main file

- **WHEN** a Library holds `<title>/<title>.mkv` and `<title>/00019.m2ts`
- **THEN** `00019.m2ts` is an Extra linked to the main Media of `<title>.mkv`, and only that main
  Media appears in browse

#### Scenario: Folder of stream files only

- **WHEN** a folder below the Library root holds only `00001.m2ts` and `00002.m2ts`
- **THEN** both files import as main Media

#### Scenario: Stream file in the Library root

- **WHEN** `00005.m2ts` sits directly in the Library root beside other titled files
- **THEN** it imports as main Media

#### Scenario: Folder with several main Media

- **WHEN** the stream file's folder holds titled files of two different main Media
- **THEN** the stream file is an Extra linked to neither

#### Scenario: Rescan reclassifies a stored stream file

- **WHEN** a disc stream file was stored as main Media and a scan finds a titled video file beside
  it
- **THEN** the file becomes an Extra of that folder's main Media, keeping its Media ID, and leaves
  browse; nothing is deleted
