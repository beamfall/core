## 1. Implementation

- [x] 1.1 Retry 503 with Retry-After in place under the existing cap; cap honoured waits at 10s
- [x] 1.2 Log the provider error when entity enrichment pauses
- [x] 1.3 Table test for 503 with Retry-After, without it, and with an over-cap wait
