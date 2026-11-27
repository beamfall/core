# Harden the feed directory proxy

Ticket CORE-2970 (`queue:core:main`).

## Why

The feed directory forwards repeated searches upstream and treats website links as search terms. Cache repeated searches, bound uncached upstream traffic, and resolve pasted website links through the server's hardened fetcher.

## What changes

- Share in-flight work for equivalent searches while any caller is waiting and cache successful results in memory for ten minutes using a bounded cache shared by sessions.
- Budget upstream requests across providers and URL discovery. Persist each token deadline atomically before sending its request and retain unexpired deadlines across restarts. Load the budget lazily and retry a failed read on the next request. Return the existing Graph `THROTTLED` code when a required request has no token.
- Use the release version and Beamfall bot URL to identify outbound directory and polling requests. The optional extended directory uses operator-provided credentials.
- Let the owning plugin resolve directory page links, such as Apple Podcasts show pages, through the server's budgeted, SSRF-checked fetcher; Core keeps no provider hostnames. Find RSS alternate links in website HTML and resolve them against the document's effective base URL. Reject invalid URL inputs locally and filter unsafe feed URLs from directory results and website discovery.
- Keep the existing Graph field and result shape. Clients should debounce incremental text input and use the structured error code to handle throttling.

## Verification and rollback

Use a fake clock for cache and rate limits, and local HTTP fixtures for discovery, upstream admission, credentials, and the User-Agent. The budget file holds only token deadlines. Earlier builds ignore it, so rollback requires no data conversion.
