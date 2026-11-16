## ADDED Requirements

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
