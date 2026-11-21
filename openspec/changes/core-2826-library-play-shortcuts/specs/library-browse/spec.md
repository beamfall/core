## ADDED Requirements

### Requirement: Web reopens a Library on its last pivot

The web SHALL remember, per Library navigation node in that browser, the last pivot shown. The
sidebar link to a Library SHALL open its remembered pivot, else Recommended. An address that names a
pivot, including the Library's own address for Recommended, SHALL still open that pivot. Blocked
storage or an unknown stored value SHALL fall back to Recommended.

#### Scenario: Returning to the Library pivot

- **WHEN** a viewer opens a Library's Library pivot, goes Home, then picks that Library in the sidebar
- **THEN** `/n/:nodeId/all` opens with the Library pivot marked current

#### Scenario: Explicit address wins

- **WHEN** the remembered pivot is Library and the viewer opens `/n/:nodeId` directly
- **THEN** the Recommended pivot opens

#### Scenario: Storage blocked

- **WHEN** browser storage throws on read
- **THEN** the sidebar link opens `/n/:nodeId`
