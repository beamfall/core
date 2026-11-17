# Browse a Library by folder

Ticket: CORE-2359 (`queue:core:main`)

## Why

A viewer sees a Library as one flat grid sorted by title or date. An Archive built from home
recordings or nested exports keeps its meaning in its folders ("2019/Trips/Coast"), and a flat
grid loses that. The owner asked to see Media by folder, "especially important on an Archive that
may have nested videos".

## What changes

- A session-tier Graph read `libraryFolder(libraryID: String!, path: String, limit: Int, offset: Int)`
  returns one folder of one Library: `path`, `rootCount`, `crumbs { path name }`,
  `folders { path name mediaCount }`, `total` and one page of `items` (the ordinary `SourceItem`
  cards).
- No `path` lists the Library's roots that hold visible Media. A Library with a single such root
  opens it directly.
- Folders come from the catalog paths of the Media the session can already see, with the visibility
  of `libraryItems`: reveal state, library allow-list, certification ceiling, trash, extras and
  per-Profile Mute. The read never lists the filesystem.
- The web Library page gains a Folders view beside Grid and List. It lists the roots, then the
  open folder's subfolders and Media, with breadcrumbs. Each folder is a link
  (`?view=folders&folder=<path>`), so Back walks up a level and a folder can be shared.
- New web strings in every shipped locale.

## Non-goals

- No change to `folderTree`, which stays a manager read of the server's filesystem.
- No folder-level actions (play folder, mark folder watched). A folder's Media cards carry the
  usual card actions.
- No persisted per-Library default to the folder view; Grid and List keep their stored choice.

## Failure modes

- A `path` outside every configured root of the Library, a relative path, or one that escapes a
  root with `..` is a validation error returned to the client. A missing `libraryID` is too.
- A Library the session may not see answers "unknown library", the same as an unknown id.
- A `path` inside a root that holds no visible Media is an empty folder, so the answer never
  tells a caller what the server holds but hides.

## Wire contract

The development wire contract gains the read. Publishing the next contract version is the landing
owner's step; until then `TestWireContractPublishedArtifactFreshness` reports the drift.
