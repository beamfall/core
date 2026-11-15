# Resumed setup reopens a lapsed admin window

Ticket: CORE-1731 (`queue:core:main`).

## Why

A setup wizard resumed after the admin window closed could not read the saved plugin choices: the
server refused the read with "admin session required", and the Plugins step showed a generic error
whose Retry could never succeed. The owner had no way forward from the wizard.

## What Changes

- When the saved plugin choices are refused for want of an admin session, the Plugins step asks for
  the admin password, reopens the admin session with it and reloads the saved choices.
- A wrong password shows the existing "not accepted" message and clears the field; other load
  failures keep the existing error with Retry.

## Non-goals

- No server change: the admin-session verify endpoint already exists.
