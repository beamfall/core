# console-transcoder-resources Specification

## Purpose
The Console transcoder resource settings let an operator choose where transcodes write their temporary files and how much the server may transcode at once, with a folder picker for the directory and a visible result when the settings are saved.

## Requirements

### Requirement: Transcoder form picks a folder and confirms a save

The Console transcoder form SHALL offer the server folder picker for the temp directory, opened at
the entered directory or else the one in effect. It SHALL disable Save while a write is in flight
and SHALL announce a successful save in a status region until the form is edited again.

#### Scenario: The temp directory is picked

- **WHEN** a manager presses Browse, opens a folder and presses "Use this folder"
- **THEN** the temp directory field holds that folder's path

#### Scenario: A save is confirmed

- **WHEN** a manager saves the form and the write succeeds
- **THEN** the form shows "Transcoding settings saved"
- **AND** the message clears when a field changes
