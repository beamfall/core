## ADDED Requirements

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
