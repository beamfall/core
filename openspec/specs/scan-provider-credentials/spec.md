# scan-provider-credentials Specification

## Purpose
How a scan reports a metadata provider credential that is missing: once per scan, naming the plugin and credential, never an internal credential ref.

## Requirements

### Requirement: A scan reports a missing provider credential once

When auto-identify fails because a provider credential is not configured, the scan SHALL report
that credential once per scan, naming the credential and where to add it, and SHALL NOT log a
per-file error or the credential's filesystem path. The scan result and the completed job's
message SHALL name the missing credential. Other identification failures SHALL still be logged
per file.

#### Scenario: Library scanned without a provider key

- **WHEN** a Library of many files is scanned and its provider's credential is not configured
- **THEN** the files import unidentified, one WARN names the missing credential, and the job's
  completion message names it

#### Scenario: Scan job resumes after yielding

- **WHEN** a scan job that already reported a missing credential yields and resumes
- **THEN** the resumed run does not report that credential again

#### Scenario: Another identification failure

- **WHEN** identification fails for a reason other than a missing credential
- **THEN** each affected file is logged as before

### Requirement: Scan status names a missing credential by its provider

The completed scan job's message SHALL name each missing provider credential by the owning
plugin's display name and the credential's label, and SHALL say where to add it. It SHALL NOT show
the internal scoped credential ref. A credential no registered plugin declares SHALL be named
generically.

#### Scenario: Plugin with a labelled credential

- **WHEN** a scan ends with a missing credential whose scraper declares a credential label
- **THEN** the job message names `<plugin display name>: <label>` and says to add it in the console
  under Plugins, without the `bfplugin-` ref

#### Scenario: Credential no plugin declares

- **WHEN** a scan ends with a missing credential ref that no registered plugin declares
- **THEN** the job message names it generically and still does not show the ref

### Requirement: A scan reports items identification left without a match

When auto-identify applies no match to an item, the scan SHALL count it, grouped by plugin,
scraper, parsed class and reason, and SHALL log each group once when the scan finishes with its
count and one example path, never per file. Queued items SHALL log at INFO. Counts SHALL survive
scan job yields.

#### Scenario: Items below the match threshold

- **WHEN** a scan queues items for review because no candidate cleared the threshold
- **THEN** one INFO line per plugin and scraper gives the count, and the completion message is
  unchanged

#### Scenario: Scan job yields

- **WHEN** a scan job yields and resumes
- **THEN** the finished scan reports the items of every run in one line per group

### Requirement: A scan names items that can never match as set up

Items skipped because the plugin declares no searchable scraper for their class, or because the
plugin is not loaded, SHALL log at WARN, and the completed job's message SHALL name them with their
count, class and plugin.

#### Scenario: Plugin with no scraper for the parsed class

- **WHEN** a Library is scanned whose plugin declares no searchable scraper for its items' class
- **THEN** one WARN names the plugin, class, count and an example path, and the job's completion
  message says how many items of that class could not be identified
