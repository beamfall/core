# Compatibility identity and refresh (CORE-2680)

## Why

Local integrations need to identify the server and request a library scan.
ADR-0171 governs this compatibility surface.

## What changes

- Add public and token-authenticated server information with Beamfall's product name, build version,
  configured name and existing discovery ID.
- Accept integration session credentials from the compatibility headers and return one fixed
  authentication failure.
- Rate-limit authenticated library refresh requests and route accepted work through the existing
  scan queue.
