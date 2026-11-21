# Hide the Mixed Film & TV type from the add-library picker

Ticket: CORE-2927 (`queue:core:main`).
Status: owner decision 2026-11-21.

## Why

Creating a library offered three types for the one Film & TV plugin: Films, TV Shows and Mixed.
Mixed is the plugin's default scope: it guesses movie or episode for each file, so it only helps a
folder that holds both, and it is where misidentification concentrates. Two clear choices are
easier to pick than three overlapping ones.

## What Changes

- The shared library-type list stops offering the Mixed preset, so first-run setup and Console >
  Libraries offer Films and TV Shows for the Film & TV plugin.
- The plugin keeps Mixed as its default scope. Existing Mixed libraries, and `createLibrary` calls
  that name it or leave the scope out, behave as before.
- The Film & TV glossary entry says Mixed is no longer offered when creating a library.
