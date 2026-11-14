# live-watch-recovery Specification

## Purpose
Keeps live filesystem watching healthy: a watcher that flaps or loses its root re-arms with bounded backoff instead of going silent.

## Requirements

### Requirement: Repeated watch failures back off

The server SHALL re-establish a Library root's failed live watch after a delay that doubles with
each consecutive failure up to a fixed cap, and SHALL only return to the shortest delay after a
re-established watch has stayed up for the stability window.

#### Scenario: Watch dies again right after restarting

- **WHEN** a root's watch is re-established and fails again before the stability window has passed
- **THEN** the next restart waits longer than the previous one, up to the cap

#### Scenario: Watch stays up

- **WHEN** a re-established watch has stayed up for the stability window and then fails
- **THEN** the next restart waits the shortest delay

### Requirement: Watch failure warnings are rate limited

The server SHALL write at most one degrade warning per root per warning interval, SHALL report on
that warning how many failures it did not warn about since the previous one, and SHALL write the
event-queue overflow line at most once per root per warning interval. The watch status the server
reports SHALL reflect every failure regardless of what was logged.

#### Scenario: Flapping root

- **WHEN** a root's watch fails several times within one warning interval
- **THEN** one warning is written and the root's status still reports each outage

#### Scenario: Next interval

- **WHEN** the root fails again after the warning interval has passed
- **THEN** a warning is written that carries the count of failures not warned about

#### Scenario: Repeated overflow

- **WHEN** a root's event queue overflows several times within one warning interval
- **THEN** each overflow is recovered and one overflow line is written
