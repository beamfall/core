## ADDED Requirements

### Requirement: Job log progress entries are readable

A job's progress history entry SHALL store the update's current count, total, unit and message. The
Console job log SHALL show a progress entry as the job's progress line rather than its stored
payload, and SHALL show a payload it does not recognise as stored.

#### Scenario: A progress entry shows what the job was doing

- **WHEN** a scan reports 120 of 340 files with the message "Scanning"
- **THEN** its job log entry reads as 120/340 files and "Scanning"

#### Scenario: A status-only payload adds nothing

- **WHEN** an entry's payload only repeats its status
- **THEN** the log shows the entry's type and time and no payload
