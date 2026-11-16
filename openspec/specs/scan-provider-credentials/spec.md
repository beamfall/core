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
