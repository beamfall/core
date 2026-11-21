# Harden the feed directory proxy

Ticket CORE-2970 (`queue:core:main`).

## Why

The feed directory forwards repeated searches upstream and treats website links as search terms. Cache repeated searches, bound uncached upstream traffic, and resolve pasted website links through the server's hardened fetcher.

## What changes

- Share in-flight work for equivalent searches and cache successful results in memory for ten minutes using a bounded cache shared by sessions.
- Budget upstream requests across providers and URL discovery. Return the existing Graph `THROTTLED` code when a required request has no token.
- Use the release version and Beamfall bot URL to identify outbound directory and polling requests. The optional extended directory uses operator-provided credentials.
- Find RSS alternate links in website HTML and resolve them against the document's effective base URL. Reject malformed HTTP(S) inputs locally and filter unsafe feed URLs from directory results and website discovery.
- Keep the existing Graph field and result shape. Clients should debounce incremental text input and use the structured error code to handle throttling.

## Verification and rollback

Use a fake clock for cache and rate limits, and local HTTP fixtures for discovery, upstream admission, credentials, and the User-Agent. Reverting the implementation restores the prior behavior. The cache and request budget live in memory, so rollback requires no data conversion.
